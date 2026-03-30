package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
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
	var downloadable whatsmeow.DownloadableMessage
	switch msg.MediaType {
	case "image":
		downloadable = &waProto.ImageMessage{
			MediaKey:      msg.MediaKey,
			FileSHA256:    msg.FileSHA256,
			FileEncSHA256: msg.FileEncSHA256,
			FileLength:    uint64Ptr(msg.FileLength),
			URL:           stringPtr(msg.MediaURL),
			DirectPath:    stringPtr(msg.DirectPath),
			Mimetype:      stringPtr(msg.MimeType),
		}
	case "video":
		downloadable = &waProto.VideoMessage{
			MediaKey:      msg.MediaKey,
			FileSHA256:    msg.FileSHA256,
			FileEncSHA256: msg.FileEncSHA256,
			FileLength:    uint64Ptr(msg.FileLength),
			URL:           stringPtr(msg.MediaURL),
			DirectPath:    stringPtr(msg.DirectPath),
			Mimetype:      stringPtr(msg.MimeType),
		}
	case "audio":
		downloadable = &waProto.AudioMessage{
			MediaKey:      msg.MediaKey,
			FileSHA256:    msg.FileSHA256,
			FileEncSHA256: msg.FileEncSHA256,
			FileLength:    uint64Ptr(msg.FileLength),
			URL:           stringPtr(msg.MediaURL),
			DirectPath:    stringPtr(msg.DirectPath),
			Mimetype:      stringPtr(msg.MimeType),
		}
	case "document":
		downloadable = &waProto.DocumentMessage{
			MediaKey:      msg.MediaKey,
			FileSHA256:    msg.FileSHA256,
			FileEncSHA256: msg.FileEncSHA256,
			FileLength:    uint64Ptr(msg.FileLength),
			URL:           stringPtr(msg.MediaURL),
			DirectPath:    stringPtr(msg.DirectPath),
			Mimetype:      stringPtr(msg.MimeType),
		}
	case "sticker":
		downloadable = &waProto.StickerMessage{
			MediaKey:      msg.MediaKey,
			FileSHA256:    msg.FileSHA256,
			FileEncSHA256: msg.FileEncSHA256,
			FileLength:    uint64Ptr(msg.FileLength),
			URL:           stringPtr(msg.MediaURL),
			DirectPath:    stringPtr(msg.DirectPath),
			Mimetype:      stringPtr(msg.MimeType),
		}
	default:
		writeError(w, http.StatusBadRequest, "NOT_MEDIA", "unsupported media type: "+msg.MediaType)
		return
	}

	data, err := s.connMgr.Client.Download(r.Context(), downloadable)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_ERROR", err.Error())
		return
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
