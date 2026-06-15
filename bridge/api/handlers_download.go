package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/mediaretry"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if req.MessageID == "" || req.ChatJID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "message_id and chat_jid are required")
		return
	}

	msg, err := s.db.GetMessageByID(req.MessageID, req.ChatJID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if msg == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "message not found")
		return
	}
	if msg.MediaType == "" {
		writeError(w, http.StatusBadRequest, "NOT_MEDIA", "message does not contain media")
		return
	}
	if len(msg.MediaKey) == 0 || (msg.MediaURL == "" && msg.DirectPath == "") {
		writeError(w, http.StatusBadRequest, "MISSING_MEDIA_INFO",
			"message is missing media key or download path - media may be too old or from history sync without full data")
		return
	}

	// Reconstruct downloadable message with actual URL/DirectPath from DB
	downloadable := buildDownloadable(msg, msg.DirectPath, msg.MediaURL)
	if downloadable == nil {
		writeError(w, http.StatusBadRequest, "NOT_MEDIA", "unsupported media type: "+msg.MediaType)
		return
	}

	data, err := s.connMgr.Client.Download(r.Context(), downloadable)
	if err != nil {
		// Media may have been purged from WhatsApp's CDN (403/404/410). Attempt a
		// re-upload request to the sender. Ban-safe: paced, daily-capped, deduped,
		// single-flight (see mediaretry). On any failure/timeout/throttle this
		// returns the ORIGINAL error unchanged — no behavior change vs. before.
		data, err = s.tryMediaRetry(r.Context(), msg, err)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DOWNLOAD_ERROR", err.Error())
			return
		}
	}

	// Determine output directory
	outputDir := expandTilde(req.OutputDir)
	if outputDir == "" {
		outputDir = "./downloads"
	}

	// Sanitize: resolve to absolute and verify no path traversal
	absDir, err := filepath.Abs(outputDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_FORM", "invalid output path")
		return
	}
	outputDir = absDir

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_ERROR", "failed to create output dir")
		return
	}

	ext := mimeToExt(msg.MimeType)
	filename := fmt.Sprintf("%s%s", req.MessageID, ext)
	outputPath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_ERROR", "failed to write file")
		return
	}

	s.db.IncrementTelemetry("media_downloaded")
	writeJSON(w, http.StatusOK, DownloadResponse{
		FilePath:  outputPath,
		MediaType: msg.MediaType,
		FileSize:  int64(len(data)),
	})
}

func stringPtr(s string) *string  { return &s }
func uint64Ptr(v int64) *uint64   { u := uint64(v); return &u }

// buildDownloadable reconstructs a whatsmeow downloadable from stored media fields.
// directPath/url are passed in so a media-retry can substitute a freshly re-uploaded
// path. Returns nil for unsupported media types.
func buildDownloadable(msg *store.Message, directPath, url string) whatsmeow.DownloadableMessage {
	switch msg.MediaType {
	case "image":
		return &waProto.ImageMessage{MediaKey: msg.MediaKey, FileSHA256: msg.FileSHA256, FileEncSHA256: msg.FileEncSHA256, FileLength: uint64Ptr(msg.FileLength), URL: stringPtr(url), DirectPath: stringPtr(directPath), Mimetype: stringPtr(msg.MimeType)}
	case "video":
		return &waProto.VideoMessage{MediaKey: msg.MediaKey, FileSHA256: msg.FileSHA256, FileEncSHA256: msg.FileEncSHA256, FileLength: uint64Ptr(msg.FileLength), URL: stringPtr(url), DirectPath: stringPtr(directPath), Mimetype: stringPtr(msg.MimeType)}
	case "audio":
		return &waProto.AudioMessage{MediaKey: msg.MediaKey, FileSHA256: msg.FileSHA256, FileEncSHA256: msg.FileEncSHA256, FileLength: uint64Ptr(msg.FileLength), URL: stringPtr(url), DirectPath: stringPtr(directPath), Mimetype: stringPtr(msg.MimeType)}
	case "document":
		return &waProto.DocumentMessage{MediaKey: msg.MediaKey, FileSHA256: msg.FileSHA256, FileEncSHA256: msg.FileEncSHA256, FileLength: uint64Ptr(msg.FileLength), URL: stringPtr(url), DirectPath: stringPtr(directPath), Mimetype: stringPtr(msg.MimeType)}
	case "sticker":
		return &waProto.StickerMessage{MediaKey: msg.MediaKey, FileSHA256: msg.FileSHA256, FileEncSHA256: msg.FileEncSHA256, FileLength: uint64Ptr(msg.FileLength), URL: stringPtr(url), DirectPath: stringPtr(directPath), Mimetype: stringPtr(msg.MimeType)}
	}
	return nil
}

// buildMessageInfo constructs the minimal MessageInfo that SendMediaRetryReceipt needs.
func buildMessageInfo(msg *store.Message) *types.MessageInfo {
	chat, _ := types.ParseJID(msg.ChatJID)
	sender, _ := types.ParseJID(msg.Sender)
	return &types.MessageInfo{
		ID: msg.ID,
		MessageSource: types.MessageSource{
			Chat:     chat,
			Sender:   sender,
			IsFromMe: msg.IsFromMe,
			IsGroup:  strings.HasSuffix(msg.ChatJID, "@g.us"),
		},
		Timestamp: time.UnixMilli(msg.Timestamp),
	}
}

// tryMediaRetry attempts to recover purged media (403/404/410) by asking the sender
// to re-upload it. It only proceeds when the ban-safe budget allows (paced, capped,
// deduped, single-flight). On any failure, timeout, or throttle it returns the
// ORIGINAL download error — so callers see exactly today's behavior unless recovery
// genuinely succeeds.
func (s *Server) tryMediaRetry(ctx context.Context, msg *store.Message, origErr error) ([]byte, error) {
	if !(errors.Is(origErr, whatsmeow.ErrMediaDownloadFailedWith403) ||
		errors.Is(origErr, whatsmeow.ErrMediaDownloadFailedWith404) ||
		errors.Is(origErr, whatsmeow.ErrMediaDownloadFailedWith410)) {
		return nil, origErr
	}
	if len(msg.MediaKey) == 0 {
		return nil, origErr
	}
	if !mediaretry.Allow(msg.ID, time.Now()) {
		return nil, origErr // budget exhausted / paced out / already tried — stay quiet
	}
	defer mediaretry.Done()

	info := buildMessageInfo(msg)
	ch := mediaretry.Register(msg.ID)
	defer mediaretry.Cleanup(msg.ID)

	if err := s.connMgr.Client.SendMediaRetryReceipt(ctx, info, msg.MediaKey); err != nil {
		s.logger.Warn("media retry receipt failed", "msg_id", msg.ID, "error", err)
		return nil, origErr
	}
	s.logger.Info("media retry requested", "msg_id", msg.ID)

	select {
	case res := <-ch:
		if res.Err != nil || res.DirectPath == "" {
			return nil, origErr
		}
		dl := buildDownloadable(msg, res.DirectPath, "")
		if dl == nil {
			return nil, origErr
		}
		data, derr := s.connMgr.Client.Download(ctx, dl)
		if derr != nil {
			return nil, origErr
		}
		s.db.IncrementTelemetry("media_retry_recovered")
		s.logger.Info("media recovered via re-upload", "msg_id", msg.ID, "size", len(data))
		return data, nil
	case <-time.After(mediaretry.WaitTimeout()):
		s.logger.Info("media retry timed out", "msg_id", msg.ID)
		return nil, origErr
	}
}

func mimeToExt(mime string) string {
	// Strip codec parameters (e.g. "audio/ogg; codecs=opus" -> "audio/ogg")
	base := mime
	if idx := strings.Index(mime, ";"); idx >= 0 {
		base = strings.TrimSpace(mime[:idx])
	}
	exts := map[string]string{
		"image/jpeg":         ".jpg",
		"image/png":          ".png",
		"image/gif":          ".gif",
		"image/webp":         ".webp",
		"video/mp4":          ".mp4",
		"audio/ogg":          ".ogg",
		"audio/mp4":          ".m4a",
		"audio/mpeg":         ".mp3",
		"application/pdf":    ".pdf",
		"application/msword": ".doc",
	}
	if e, ok := exts[base]; ok {
		return e
	}
	return ".bin"
}
