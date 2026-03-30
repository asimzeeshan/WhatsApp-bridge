package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/media"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// getEphemeralExpiry checks if a group has disappearing messages enabled and
// returns the timer in seconds (e.g. 86400 for 24h, 604800 for 7d). Returns 0
// for non-group JIDs or groups without disappearing messages.
func (s *Server) getEphemeralExpiry(ctx context.Context, jid types.JID) uint32 {
	if jid.Server != types.GroupServer {
		return 0
	}
	if s.connMgr.Client == nil {
		return 0
	}
	info, err := s.connMgr.Client.GetGroupInfo(ctx, jid)
	if err != nil {
		s.logger.Warn("failed to get group info for ephemeral check", "jid", jid, "error", err)
		return 0
	}
	if !info.IsEphemeral {
		return 0
	}
	return info.DisappearingTimer
}

// injectEphemeralExpiry sets ContextInfo.Expiration on a media message proto so
// it participates in the group's disappearing messages timer.
func injectEphemeralExpiry(msg *waProto.Message, expiry uint32) {
	if expiry == 0 {
		return
	}
	// Helper to ensure ContextInfo exists and set Expiration
	setExpiry := func(ci **waProto.ContextInfo) {
		if *ci == nil {
			*ci = &waProto.ContextInfo{}
		}
		(*ci).Expiration = proto.Uint32(expiry)
	}

	if m := msg.GetImageMessage(); m != nil {
		setExpiry(&m.ContextInfo)
	}
	if m := msg.GetVideoMessage(); m != nil {
		setExpiry(&m.ContextInfo)
	}
	if m := msg.GetAudioMessage(); m != nil {
		setExpiry(&m.ContextInfo)
	}
	if m := msg.GetDocumentMessage(); m != nil {
		setExpiry(&m.ContextInfo)
	}
	if m := msg.GetStickerMessage(); m != nil {
		setExpiry(&m.ContextInfo)
	}
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if req.To == "" || req.Text == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "to and text are required")
		return
	}

	jid, ok := normalizePhone(req.To)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid recipient")
		return
	}

	toJID := jidToTypes(jid)
	ephemeral := s.getEphemeralExpiry(r.Context(), toJID)

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(req.Text),
		},
	}

	// Simple text if no mentions, quotes, or ephemeral
	if len(req.Mentions) == 0 && req.QuotedMessageID == "" && ephemeral == 0 {
		msg = &waProto.Message{
			Conversation: proto.String(req.Text),
		}
	}

	// Handle mentions
	if len(req.Mentions) > 0 {
		mentionJIDs := make([]string, len(req.Mentions))
		copy(mentionJIDs, req.Mentions)
		msg.ExtendedTextMessage.ContextInfo = &waProto.ContextInfo{
			MentionedJID: mentionJIDs,
		}
	}

	// Handle quoting
	if req.QuotedMessageID != "" {
		if msg.ExtendedTextMessage == nil {
			msg.ExtendedTextMessage = &waProto.ExtendedTextMessage{
				Text: proto.String(req.Text),
			}
			msg.Conversation = nil
		}
		if msg.ExtendedTextMessage.ContextInfo == nil {
			msg.ExtendedTextMessage.ContextInfo = &waProto.ContextInfo{}
		}
		msg.ExtendedTextMessage.ContextInfo.StanzaID = proto.String(req.QuotedMessageID)
		if req.QuotedParticipant != "" {
			participantJID := jidToTypes(req.QuotedParticipant)
			pStr := participantJID.String()
			msg.ExtendedTextMessage.ContextInfo.Participant = &pStr
		}
		msg.ExtendedTextMessage.ContextInfo.QuotedMessage = &waProto.Message{
			Conversation: proto.String(""),
		}
	}

	// Set ephemeral expiry on text messages
	if ephemeral > 0 && msg.ExtendedTextMessage != nil {
		if msg.ExtendedTextMessage.ContextInfo == nil {
			msg.ExtendedTextMessage.ContextInfo = &waProto.ContextInfo{}
		}
		msg.ExtendedTextMessage.ContextInfo.Expiration = proto.Uint32(ephemeral)
	}

	resp, err := s.connMgr.Client.SendMessage(r.Context(), toJID, msg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", err.Error())
		return
	}

	s.db.IncrementTelemetry("messages_sent")
	writeJSON(w, http.StatusOK, SendResponse{
		Success:   true,
		MessageID: resp.ID,
	})
}

func (s *Server) handleSendMedia(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	if err := r.ParseMultipartForm(int64(s.cfg.Bridge.Media.MaxFileSizeMB) << 20); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_FORM", "failed to parse multipart form")
		return
	}

	to := r.FormValue("to")
	if to == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "to is required")
		return
	}

	jid, ok := normalizePhone(to)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid recipient")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "file is required")
		return
	}
	defer file.Close()

	maxBytes := int64(s.cfg.Bridge.Media.MaxFileSizeMB) << 20
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UPLOAD_ERROR", "failed to read file")
		return
	}
	if int64(len(data)) > maxBytes {
		writeError(w, http.StatusBadRequest, "UPLOAD_ERROR", "file exceeds maximum size")
		return
	}

	mediaType := r.FormValue("media_type")
	if mediaType == "" {
		mediaType = detectMediaType(header.Filename)
	}
	caption := r.FormValue("caption")
	ptt := r.FormValue("ptt") == "true"

	toJID := jidToTypes(jid)
	ephemeral := s.getEphemeralExpiry(r.Context(), toJID)
	var resp whatsmeow.SendResponse
	var sendErr error

	uploaded, err := s.connMgr.Client.Upload(r.Context(), data, mediaTypeToWM(mediaType))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UPLOAD_ERROR", err.Error())
		return
	}

	var msg *waProto.Message
	switch mediaType {
	case "image":
		msg = &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				URL:           &uploaded.URL,
				DirectPath:    &uploaded.DirectPath,
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
				Mimetype:      proto.String(detectMimeType(header.Filename)),
				Caption:       proto.String(caption),
			},
		}
	case "video":
		msg = &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				URL:           &uploaded.URL,
				DirectPath:    &uploaded.DirectPath,
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
				Mimetype:      proto.String(detectMimeType(header.Filename)),
				Caption:       proto.String(caption),
			},
		}
	case "audio":
		mimetype := "audio/mp4"
		if ptt {
			mimetype = "audio/ogg; codecs=opus"
		}
		audioMsg := &waProto.AudioMessage{
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(mimetype),
			PTT:           proto.Bool(ptt),
		}
		// Analyze OGG Opus for waveform and duration (PTT voice notes)
		if ptt {
			if dur, waveform, err := media.AnalyzeOggOpus(data); err == nil {
				audioMsg.Seconds = proto.Uint32(dur)
				audioMsg.Waveform = waveform
			} else {
				s.logger.Debug("ogg opus analysis failed, sending without waveform", "error", err)
			}
		}
		msg = &waProto.Message{AudioMessage: audioMsg}
	default: // document
		msg = &waProto.Message{
			DocumentMessage: &waProto.DocumentMessage{
				URL:           &uploaded.URL,
				DirectPath:    &uploaded.DirectPath,
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
				Mimetype:      proto.String(detectMimeType(header.Filename)),
				FileName:      proto.String(header.Filename),
				Caption:       proto.String(caption),
			},
		}
	}

	injectEphemeralExpiry(msg, ephemeral)
	resp, sendErr = s.connMgr.Client.SendMessage(r.Context(), toJID, msg)

	if sendErr != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", sendErr.Error())
		return
	}

	s.db.IncrementTelemetry("media_sent")
	writeJSON(w, http.StatusOK, SendResponse{
		Success:   true,
		MessageID: resp.ID,
	})
}

func detectMediaType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "image"
	case ".mp4", ".avi", ".mkv", ".mov":
		return "video"
	case ".mp3", ".ogg", ".wav", ".m4a":
		return "audio"
	default:
		return "document"
	}
}

func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	mimes := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp",
		".mp4": "video/mp4", ".avi": "video/x-msvideo", ".mov": "video/quicktime",
		".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".m4a": "audio/mp4",
		".pdf": "application/pdf",
	}
	if m, ok := mimes[ext]; ok {
		return m
	}
	return "application/octet-stream"
}

func mediaTypeToWM(mediaType string) whatsmeow.MediaType {
	switch mediaType {
	case "image":
		return whatsmeow.MediaImage
	case "video":
		return whatsmeow.MediaVideo
	case "audio":
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

// expandTilde replaces ~ with the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// handleSendReaction sends an emoji reaction to a message.
func (s *Server) handleSendReaction(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	var req ReactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if req.ChatJID == "" || req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "chat_jid and message_id are required")
		return
	}
	if !isValidJID(req.ChatJID) {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid chat_jid")
		return
	}

	chatJID := jidToTypes(req.ChatJID)
	senderJID := types.EmptyJID
	if req.Sender != "" {
		senderJID = jidToTypes(req.Sender)
	}

	reactionMsg := s.connMgr.Client.BuildReaction(chatJID, senderJID, req.MessageID, req.Emoji)
	resp, err := s.connMgr.Client.SendMessage(r.Context(), chatJID, reactionMsg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", err.Error())
		return
	}

	s.db.IncrementTelemetry("reactions_sent")
	writeJSON(w, http.StatusOK, SendResponse{
		Success:   true,
		MessageID: resp.ID,
	})
}

// handleEditMessage edits a previously sent message.
func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	var req EditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if req.ChatJID == "" || req.MessageID == "" || req.NewText == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "chat_jid, message_id, and new_text are required")
		return
	}
	if !isValidJID(req.ChatJID) {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid chat_jid")
		return
	}

	chatJID := jidToTypes(req.ChatJID)
	newContent := &waProto.Message{
		Conversation: proto.String(req.NewText),
	}

	editMsg := s.connMgr.Client.BuildEdit(chatJID, req.MessageID, newContent)
	resp, err := s.connMgr.Client.SendMessage(r.Context(), chatJID, editMsg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", err.Error())
		return
	}

	// Update the message content in the database
	s.db.UpdateEditedContent(req.MessageID, req.ChatJID, req.NewText)

	s.db.IncrementTelemetry("messages_edited")
	writeJSON(w, http.StatusOK, SendResponse{
		Success:   true,
		MessageID: resp.ID,
	})
}

// handleRevokeMessage deletes/revokes a message for everyone.
func (s *Server) handleRevokeMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	var req RevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if req.ChatJID == "" || req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "chat_jid and message_id are required")
		return
	}
	if !isValidJID(req.ChatJID) {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid chat_jid")
		return
	}

	chatJID := jidToTypes(req.ChatJID)
	senderJID := types.EmptyJID
	if req.Sender != "" {
		senderJID = jidToTypes(req.Sender)
	}

	revokeMsg := s.connMgr.Client.BuildRevoke(chatJID, senderJID, req.MessageID)
	resp, err := s.connMgr.Client.SendMessage(r.Context(), chatJID, revokeMsg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", err.Error())
		return
	}

	// Mark the message as revoked in the database
	s.db.MarkRevoked(req.MessageID, req.ChatJID)

	s.db.IncrementTelemetry("messages_revoked")
	writeJSON(w, http.StatusOK, SendResponse{
		Success:   true,
		MessageID: resp.ID,
	})
}
