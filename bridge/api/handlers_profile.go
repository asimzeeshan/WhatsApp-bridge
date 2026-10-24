package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// ProfilePictureResponse describes the profile picture of a user or group.
type ProfilePictureResponse struct {
	JID        string `json:"jid"`
	HasPicture bool   `json:"has_picture"`
	Reason     string `json:"reason,omitempty"`
	PictureID  string `json:"picture_id,omitempty"`
	Type       string `json:"type,omitempty"`
	URL        string `json:"url,omitempty"`
	FilePath   string `json:"file_path,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
}

// maxProfilePictureBytes caps the image download (full-res pictures are well under this).
const maxProfilePictureBytes = 10 << 20

var safeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// handleGetProfilePicture returns profile picture info for a JID.
//
// GET /api/profile-picture/{jid}
//
//	jid         user/group JID, or "me" for the bridge's own account
//	preview     "true" for the thumbnail instead of full resolution
//	download    "true" to save the image to output_dir and return file_path
//	output_dir  where to save the image (default ./media/profile)
func (s *Server) handleGetProfilePicture(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnected(w) {
		return
	}

	jidStr := strings.TrimSpace(chi.URLParam(r, "jid"))
	var target types.JID
	if jidStr == "" || jidStr == "me" {
		if s.connMgr.Client.Store == nil || s.connMgr.Client.Store.ID == nil {
			writeError(w, http.StatusServiceUnavailable, "NOT_LOGGED_IN", "own JID is not available")
			return
		}
		target = s.connMgr.Client.Store.ID.ToNonAD()
	} else {
		parsed, err := types.ParseJID(jidStr)
		if err != nil || parsed.User == "" {
			writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid jid: "+jidStr)
			return
		}
		target = parsed.ToNonAD()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	resp := ProfilePictureResponse{JID: target.String()}

	info, err := s.connMgr.Client.GetProfilePictureInfo(ctx, target, &whatsmeow.GetProfilePictureParams{
		Preview: boolParam(r, "preview"),
	})
	switch {
	case errors.Is(err, whatsmeow.ErrProfilePictureNotSet):
		resp.Reason = "not_set"
		writeJSON(w, http.StatusOK, resp)
		return
	case errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized):
		resp.Reason = "hidden"
		writeJSON(w, http.StatusOK, resp)
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "PROFILE_PICTURE_ERROR", err.Error())
		return
	case info == nil:
		// whatsmeow returns (nil, nil) when the picture is unchanged from ExistingID;
		// we never pass ExistingID, so treat this as "not set".
		resp.Reason = "not_set"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp.HasPicture = true
	resp.PictureID = info.ID
	resp.Type = info.Type
	resp.URL = info.URL

	if boolParam(r, "download") && info.URL != "" {
		path, size, err := downloadProfilePicture(ctx, info.URL, stringParam(r, "output_dir"), target, info.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DOWNLOAD_ERROR", err.Error())
			return
		}
		resp.FilePath = path
		resp.FileSize = size
	}

	writeJSON(w, http.StatusOK, resp)
}

func downloadProfilePicture(ctx context.Context, url, outputDir string, jid types.JID, pictureID string) (string, int64, error) {
	outputDir = expandTilde(outputDir)
	if outputDir == "" {
		outputDir = "./media/profile"
	}
	absDir, err := filepath.Abs(outputDir)
	if err != nil {
		return "", 0, fmt.Errorf("invalid output path")
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		return "", 0, fmt.Errorf("failed to create output dir")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("image download returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxProfilePictureBytes+1))
	if err != nil {
		return "", 0, err
	}
	if len(data) > maxProfilePictureBytes {
		return "", 0, fmt.Errorf("image exceeds %d bytes", maxProfilePictureBytes)
	}

	name := safeFilenameChars.ReplaceAllString(jid.User+"_"+pictureID, "_") + ".jpg"
	path := filepath.Join(absDir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", 0, fmt.Errorf("failed to write file")
	}
	return path, int64(len(data)), nil
}
