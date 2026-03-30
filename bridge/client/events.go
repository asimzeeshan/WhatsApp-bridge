package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/indexer"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

// rawLogMu protects concurrent writes to the raw log file.
var rawLogMu sync.Mutex

// dumpRawMessage appends the full message event as a JSON line to logs/raw/YYYY-MM-DD.jsonl.
func dumpRawMessage(dataDir string, evt *events.Message) {
	rawDir := filepath.Join(dataDir, "..", "logs", "raw")
	os.MkdirAll(rawDir, 0755)

	filename := time.Now().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(rawDir, filename)

	entry := map[string]any{
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
		"message_id":  evt.Info.ID,
		"chat":        evt.Info.Chat.String(),
		"sender":      evt.Info.Sender.String(),
		"push_name":   evt.Info.PushName,
		"is_from_me":  evt.Info.IsFromMe,
		"timestamp_ms": evt.Info.Timestamp.UnixMilli(),
		"message":     evt.RawMessage,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	rawLogMu.Lock()
	defer rawLogMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(line)
}

func (b *Bridge) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		b.handleMessage(v)
	case *events.HistorySync:
		b.handleHistorySync(v)
	case *events.PushNameSetting:
		b.Logger.Info("push name updated", "name", v.Action.GetName())
	case *events.PushName:
		b.DB.UpsertContact(&store.Contact{
			JID:    v.JID.String(),
			Notify: v.NewPushName,
		})
	case *events.TemporaryBan:
		b.Logger.Error("temporary ban", "code", v.Code, "expire", v.Expire)
		if b.OnPermanentDisconnect != nil {
			b.OnPermanentDisconnect("temporary_ban", true)
		}
	case *events.LoggedOut:
		b.Logger.Error("logged out", "reason", v.Reason)
		if b.OnPermanentDisconnect != nil {
			b.OnPermanentDisconnect("logged_out", false)
		}
	case *events.ClientOutdated:
		b.Logger.Error("client outdated — update whatsmeow")
		if b.OnPermanentDisconnect != nil {
			b.OnPermanentDisconnect("client_outdated", true)
		}
	case *events.StreamReplaced:
		b.Logger.Warn("stream replaced by another connection")
	case *events.KeepAliveTimeout:
		b.Logger.Warn("keepalive timeout", "error_count", v.ErrorCount)
	}
}

func (b *Bridge) handleMessage(evt *events.Message) {
	// Raw dump first - never lose a message
	go dumpRawMessage(b.Cfg.Bridge.DataDir, evt)

	chatJID := evt.Info.Chat.String()
	senderJID := evt.Info.Sender.String()
	senderName := evt.Info.PushName
	timestamp := evt.Info.Timestamp.UnixMilli()
	isFromMe := evt.Info.IsFromMe

	// Handle protocol messages (revoke/delete) separately
	if pmsg := evt.Message.GetProtocolMessage(); pmsg != nil {
		if pmsg.GetType() == waE2E.ProtocolMessage_REVOKE {
			if key := pmsg.GetKey(); key != nil {
				targetID := key.GetID()
				b.DB.MarkRevoked(targetID, chatJID)
				b.Logger.Debug("message revoked", "target", targetID, "chat", chatJID, "by", senderName)
			}
			return
		}
	}

	// Handle edited messages
	if edited := evt.Message.GetEditedMessage(); edited != nil {
		if innerMsg := edited.GetMessage(); innerMsg != nil {
			if innerProto := innerMsg.GetProtocolMessage(); innerProto != nil {
				if key := innerProto.GetKey(); key != nil {
					targetID := key.GetID()
					newContent := ""
					if editedMsg := innerProto.GetEditedMessage(); editedMsg != nil {
						if editedMsg.GetConversation() != "" {
							newContent = editedMsg.GetConversation()
						} else if editedMsg.GetExtendedTextMessage() != nil {
							newContent = editedMsg.GetExtendedTextMessage().GetText()
						}
					}
					if newContent != "" {
						b.DB.UpdateEditedContent(targetID, chatJID, newContent)
						b.Logger.Debug("message edited", "target", targetID, "chat", chatJID, "by", senderName)
					}
				}
			}
		}
		return
	}

	// Handle reactions separately - they are not regular messages
	if reaction := evt.Message.GetReactionMessage(); reaction != nil {
		targetID := reaction.GetKey().GetID()
		emoji := reaction.GetText()
		b.DB.UpsertReaction(&store.Reaction{
			MessageID:   targetID,
			ChatJID:     chatJID,
			ReactorJID:  senderJID,
			ReactorName: senderName,
			Emoji:       emoji,
			Timestamp:   timestamp,
		})
		b.Logger.Debug("reaction", "emoji", emoji, "target", targetID, "from", senderName)
		return // reactions are not messages - don't store in messages table
	}

	// Extract text content
	content := extractText(evt)

	// Determine media type
	mediaType, mimeType, filename, fileLength := extractMediaInfo(evt)
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var mediaURL, directPath string
	if mediaType != "" {
		mediaKey, fileSHA256, fileEncSHA256 = extractMediaKeys(evt)
		mediaURL, directPath = extractMediaDownloadInfo(evt)
	}

	// Extract quoted message info
	quotedID, quotedParticipant := extractQuoteInfo(evt)

	msg := &store.Message{
		ID:                evt.Info.ID,
		ChatJID:           chatJID,
		Sender:            senderJID,
		SenderName:        senderName,
		Content:           content,
		Timestamp:         timestamp,
		IsFromMe:          isFromMe,
		MediaType:         mediaType,
		MimeType:          mimeType,
		Filename:          filename,
		MediaKey:          mediaKey,
		FileSHA256:        fileSHA256,
		FileEncSHA256:     fileEncSHA256,
		FileLength:        fileLength,
		MediaURL:          mediaURL,
		DirectPath:        directPath,
		PushName:          senderName,
		QuotedMessageID:   quotedID,
		QuotedParticipant: quotedParticipant,
	}

	b.DB.UpsertMessage(msg)

	// Update chat
	preview := content
	if len(preview) > 100 {
		preview = preview[:100]
	}
	isGroup := strings.HasSuffix(chatJID, "@g.us")
	chatName := ""
	if !isGroup {
		// Only set chat name from push name for individual chats, not groups
		chatName = evt.Info.PushName
	}
	b.DB.UpsertChat(&store.Chat{
		JID:                chatJID,
		Name:               chatName,
		IsGroup:            isGroup,
		LastMessageTime:    fmt.Sprintf("%d", timestamp),
		LastMessagePreview: preview,
	})

	if !isFromMe {
		b.DB.IncrementUnread(chatJID)
		b.DB.IncrementTelemetry("messages_received")
	}

	// Index links
	if content != "" {
		links := indexer.ExtractLinks(content)
		for _, link := range links {
			b.DB.InsertLink(&store.Link{
				URL:       link.URL,
				Platform:  link.Platform,
				SenderJID: senderJID,
				ChatJID:   chatJID,
				MessageID: evt.Info.ID,
				Timestamp: timestamp,
			})
			b.DB.IncrementTelemetry("links_indexed")
		}
	}

	// Auto-download images from ALL chats (groups + DMs)
	if mediaType == "image" && b.Cfg.Bridge.Media.AutoDownloadImages {
		go b.autoDownloadImage(evt)
	}

	// Auto-download audio from ALL chats (groups + DMs)
	if mediaType == "audio" && b.Cfg.Bridge.Media.AutoDownloadAudio {
		go b.autoDownloadAudio(evt)
	}

	// Auto-transcribe voice notes (fast path - worker handles retries)
	if mediaType == "audio" && b.Transcriber != nil && b.Transcriber.IsEnabled() {
		go b.transcribeAudio(evt)
	}

	// Upsert contact from sender
	if senderName != "" {
		b.DB.UpsertContact(&store.Contact{
			JID:    senderJID,
			Notify: senderName,
		})
	}
}

// dumpHistorySyncMessage appends a history sync message to JSONL so nothing is lost.
func dumpHistorySyncMessage(dataDir, msgID, chatJID, sender, pushName, content string, ts int64, isFromMe bool) {
	rawDir := filepath.Join(dataDir, "..", "logs", "raw")
	os.MkdirAll(rawDir, 0755)

	filename := time.Now().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(rawDir, filename)

	entry := map[string]any{
		"timestamp":    time.Now().UTC().Format(time.RFC3339Nano),
		"message_id":   msgID,
		"chat":         chatJID,
		"sender":       sender,
		"push_name":    pushName,
		"is_from_me":   isFromMe,
		"timestamp_ms": ts,
		"source":       "history_sync",
		"message":      map[string]any{"conversation": content},
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	rawLogMu.Lock()
	defer rawLogMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(line)
}

func (b *Bridge) handleHistorySync(evt *events.HistorySync) {
	data := evt.Data
	if data == nil {
		return
	}

	convCount := 0
	msgCount := 0

	for _, conv := range data.GetConversations() {
		jid := conv.GetID()
		if jid == "" {
			continue
		}
		convCount++

		isGroup := strings.HasSuffix(jid, "@g.us")
		b.DB.UpsertChat(&store.Chat{
			JID:     jid,
			Name:    conv.GetDisplayName(),
			IsGroup: isGroup,
		})

		for _, hm := range conv.GetMessages() {
			wm := hm.GetMessage()
			if wm == nil || wm.GetMessage() == nil {
				continue
			}

			info := wm.GetMessage()
			ts := int64(wm.GetMessageTimestamp()) * 1000 // Convert to ms
			sender := wm.GetParticipant()
			if sender == "" {
				key := wm.GetKey()
				if key != nil && key.GetFromMe() {
					sender = "me"
				}
			}

			content := ""
			if info.GetConversation() != "" {
				content = info.GetConversation()
			} else if info.GetExtendedTextMessage() != nil {
				content = info.GetExtendedTextMessage().GetText()
			}

			// Extract media info from history sync (including keys + download paths)
			mediaType := ""
			mimeType := ""
			var fileLength int64
			var mediaKey, fileSHA256, fileEncSHA256 []byte
			var mediaURL, directPath string
			if img := info.GetImageMessage(); img != nil {
				mediaType = "image"
				mimeType = img.GetMimetype()
				fileLength = int64(img.GetFileLength())
				mediaKey = img.GetMediaKey()
				fileSHA256 = img.GetFileSHA256()
				fileEncSHA256 = img.GetFileEncSHA256()
				mediaURL = img.GetURL()
				directPath = img.GetDirectPath()
				if img.GetCaption() != "" && content == "" {
					content = img.GetCaption()
				}
			} else if vid := info.GetVideoMessage(); vid != nil {
				mediaType = "video"
				mimeType = vid.GetMimetype()
				fileLength = int64(vid.GetFileLength())
				mediaKey = vid.GetMediaKey()
				fileSHA256 = vid.GetFileSHA256()
				fileEncSHA256 = vid.GetFileEncSHA256()
				mediaURL = vid.GetURL()
				directPath = vid.GetDirectPath()
			} else if aud := info.GetAudioMessage(); aud != nil {
				mediaType = "audio"
				mimeType = aud.GetMimetype()
				fileLength = int64(aud.GetFileLength())
				mediaKey = aud.GetMediaKey()
				fileSHA256 = aud.GetFileSHA256()
				fileEncSHA256 = aud.GetFileEncSHA256()
				mediaURL = aud.GetURL()
				directPath = aud.GetDirectPath()
			} else if doc := info.GetDocumentMessage(); doc != nil {
				mediaType = "document"
				mimeType = doc.GetMimetype()
				fileLength = int64(doc.GetFileLength())
				mediaKey = doc.GetMediaKey()
				fileSHA256 = doc.GetFileSHA256()
				fileEncSHA256 = doc.GetFileEncSHA256()
				mediaURL = doc.GetURL()
				directPath = doc.GetDirectPath()
			} else if stk := info.GetStickerMessage(); stk != nil {
				mediaType = "sticker"
				mimeType = stk.GetMimetype()
				fileLength = int64(stk.GetFileLength())
				mediaKey = stk.GetMediaKey()
				fileSHA256 = stk.GetFileSHA256()
				fileEncSHA256 = stk.GetFileEncSHA256()
				mediaURL = stk.GetURL()
				directPath = stk.GetDirectPath()
			}

			// Handle reactions from history sync
			if reaction := info.GetReactionMessage(); reaction != nil {
				targetID := reaction.GetKey().GetID()
				emoji := reaction.GetText()
				b.DB.UpsertReaction(&store.Reaction{
					MessageID:   targetID,
					ChatJID:     jid,
					ReactorJID:  sender,
					ReactorName: wm.GetPushName(),
					Emoji:       emoji,
					Timestamp:   ts,
				})
				continue // don't store as a message
			}

			isFromMe := false
			key := wm.GetKey()
			if key != nil {
				isFromMe = key.GetFromMe()
			}

			msgID := ""
			if key != nil {
				msgID = key.GetID()
			}

			if msgID != "" {
				b.DB.UpsertMessage(&store.Message{
					ID:            msgID,
					ChatJID:       jid,
					Sender:        sender,
					SenderName:    wm.GetPushName(),
					Content:       content,
					Timestamp:     ts,
					IsFromMe:      isFromMe,
					PushName:      wm.GetPushName(),
					MediaType:     mediaType,
					MimeType:      mimeType,
					FileLength:    fileLength,
					MediaKey:      mediaKey,
					FileSHA256:    fileSHA256,
					FileEncSHA256: fileEncSHA256,
					MediaURL:      mediaURL,
					DirectPath:    directPath,
				})

				// Also index links from history sync messages
				if content != "" {
					links := indexer.ExtractLinks(content)
					for _, link := range links {
						b.DB.InsertLink(&store.Link{
							URL:       link.URL,
							Platform:  link.Platform,
							SenderJID: sender,
							ChatJID:   jid,
							MessageID: msgID,
							Timestamp: ts,
						})
					}
				}

				// Dump to JSONL so raw log is complete
				dumpHistorySyncMessage(b.Cfg.Bridge.DataDir, msgID, jid, sender, wm.GetPushName(), content, ts, isFromMe)

				msgCount++
			}
		}
	}

	if convCount > 0 || msgCount > 0 {
		b.Logger.Info("history sync", "conversations", convCount, "messages", msgCount)
	}
}

func (b *Bridge) isWatchedGroup(jid string) bool {
	for _, watched := range b.Cfg.Bridge.Monitoring.WatchedGroupJIDs {
		if strings.TrimSpace(watched) == jid {
			return true
		}
	}
	return false
}

func (b *Bridge) autoDownloadImage(evt *events.Message) {
	imgMsg := evt.Message.GetImageMessage()
	if imgMsg == nil {
		return
	}

	data, err := b.downloadMedia(evt)
	if err != nil {
		b.Logger.Warn("auto-download failed", "error", err, "msg_id", evt.Info.ID)
		return
	}

	outputDir := expandTilde(b.Cfg.Bridge.Media.ImageOutputDir)
	dateDir := filepath.Join(outputDir, time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0755); err != nil {
		b.Logger.Warn("auto-download mkdir failed", "error", err)
		return
	}

	sender := evt.Info.Sender.User
	ts := evt.Info.Timestamp.UnixMilli()
	ext := mimeToExt(imgMsg.GetMimetype())
	filename := fmt.Sprintf("%s_%d_%s%s", sender, ts, evt.Info.ID, ext)
	outputPath := filepath.Join(dateDir, filename)

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		b.Logger.Warn("auto-download write failed", "error", err)
		return
	}

	// Store local path in DB
	chatJID := evt.Info.Chat.String()
	b.DB.UpdateLocalPath(evt.Info.ID, chatJID, outputPath)

	b.DB.IncrementTelemetry("media_downloaded")
	b.Logger.Info("auto-downloaded image", "path", outputPath, "size", len(data))
}

func (b *Bridge) autoDownloadAudio(evt *events.Message) {
	audioMsg := evt.Message.GetAudioMessage()
	if audioMsg == nil {
		return
	}

	if b.waClient == nil {
		return
	}

	data, err := b.waClient.Download(context.Background(), audioMsg)
	if err != nil {
		b.Logger.Warn("audio auto-download failed", "error", err, "msg_id", evt.Info.ID)
		return
	}

	outputDir := expandTilde(b.Cfg.Bridge.Media.AudioOutputDir)
	if outputDir == "" {
		outputDir = "./media/audio"
	}
	dateDir := filepath.Join(outputDir, time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0755); err != nil {
		b.Logger.Warn("audio auto-download mkdir failed", "error", err)
		return
	}

	sender := evt.Info.Sender.User
	ts := evt.Info.Timestamp.UnixMilli()
	ext := mimeToExt(audioMsg.GetMimetype())
	filename := fmt.Sprintf("%s_%d_%s%s", sender, ts, evt.Info.ID, ext)
	outputPath := filepath.Join(dateDir, filename)

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		b.Logger.Warn("audio auto-download write failed", "error", err)
		return
	}

	// Store local path in DB for transcription worker
	chatJID := evt.Info.Chat.String()
	b.DB.UpdateLocalPath(evt.Info.ID, chatJID, outputPath)

	b.DB.IncrementTelemetry("media_downloaded")
	b.Logger.Info("auto-downloaded audio", "path", outputPath, "size", len(data))
}

func (b *Bridge) transcribeAudio(evt *events.Message) {
	if b.waClient == nil {
		return
	}

	audioMsg := evt.Message.GetAudioMessage()
	if audioMsg == nil {
		return
	}

	data, err := b.waClient.Download(context.Background(), audioMsg)
	if err != nil {
		b.Logger.Warn("transcription download failed", "error", err, "msg_id", evt.Info.ID)
		return
	}

	filename := fmt.Sprintf("%s.ogg", evt.Info.ID)
	text, err := b.Transcriber.Transcribe(data, filename)
	if err != nil {
		b.Logger.Warn("transcription failed", "error", err, "msg_id", evt.Info.ID)
		return
	}

	if text != "" {
		// Store transcription in separate column (preserves original content)
		chatJID := evt.Info.Chat.String()
		b.DB.Writer.Enqueue(
			"UPDATE messages SET transcription = ? WHERE id = ? AND chat_jid = ?",
			text, evt.Info.ID, chatJID,
		)
		b.Logger.Info("transcribed voice note", "msg_id", evt.Info.ID, "text_length", len(text))
	}
}

func (b *Bridge) downloadMedia(evt *events.Message) ([]byte, error) {
	if b.waClient == nil {
		return nil, fmt.Errorf("WhatsApp client not initialized")
	}
	img := evt.Message.GetImageMessage()
	if img == nil {
		return nil, fmt.Errorf("message has no image")
	}
	return b.waClient.Download(context.Background(), img)
}

func extractText(evt *events.Message) string {
	msg := evt.Message
	if msg == nil {
		return ""
	}
	if msg.GetConversation() != "" {
		return msg.GetConversation()
	}
	if msg.GetExtendedTextMessage() != nil {
		return msg.GetExtendedTextMessage().GetText()
	}
	if msg.GetImageMessage() != nil && msg.GetImageMessage().GetCaption() != "" {
		return msg.GetImageMessage().GetCaption()
	}
	if msg.GetVideoMessage() != nil && msg.GetVideoMessage().GetCaption() != "" {
		return msg.GetVideoMessage().GetCaption()
	}
	if msg.GetDocumentMessage() != nil && msg.GetDocumentMessage().GetCaption() != "" {
		return msg.GetDocumentMessage().GetCaption()
	}
	return ""
}

func extractMediaInfo(evt *events.Message) (mediaType, mimeType, filename string, fileLength int64) {
	msg := evt.Message
	if msg == nil {
		return
	}
	if img := msg.GetImageMessage(); img != nil {
		return "image", img.GetMimetype(), "", int64(img.GetFileLength())
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", vid.GetMimetype(), "", int64(vid.GetFileLength())
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", aud.GetMimetype(), "", int64(aud.GetFileLength())
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return "document", doc.GetMimetype(), doc.GetFileName(), int64(doc.GetFileLength())
	}
	if msg.GetStickerMessage() != nil {
		return "sticker", msg.GetStickerMessage().GetMimetype(), "", int64(msg.GetStickerMessage().GetFileLength())
	}
	return
}

func extractMediaKeys(evt *events.Message) (mediaKey, fileSHA256, fileEncSHA256 []byte) {
	msg := evt.Message
	if msg == nil {
		return
	}
	if img := msg.GetImageMessage(); img != nil {
		return img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256()
	}
	return
}

func extractMediaDownloadInfo(evt *events.Message) (mediaURL, directPath string) {
	msg := evt.Message
	if msg == nil {
		return
	}
	if img := msg.GetImageMessage(); img != nil {
		return img.GetURL(), img.GetDirectPath()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetURL(), vid.GetDirectPath()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return aud.GetURL(), aud.GetDirectPath()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetURL(), doc.GetDirectPath()
	}
	if stk := msg.GetStickerMessage(); stk != nil {
		return stk.GetURL(), stk.GetDirectPath()
	}
	return
}

func extractQuoteInfo(evt *events.Message) (quotedID, quotedParticipant string) {
	msg := evt.Message
	if msg == nil {
		return
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		if ctx := ext.GetContextInfo(); ctx != nil {
			return ctx.GetStanzaID(), ctx.GetParticipant()
		}
	}
	return
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func mimeToExt(mime string) string {
	base := mime
	if idx := strings.Index(mime, ";"); idx >= 0 {
		base = strings.TrimSpace(mime[:idx])
	}

	exts := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif",
		"image/webp": ".webp", "video/mp4": ".mp4", "audio/ogg": ".ogg",
		"audio/mp4": ".m4a", "audio/mpeg": ".mp3",
	}
	if e, ok := exts[base]; ok {
		return e
	}
	return ".bin"
}
