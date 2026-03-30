package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/connection"
	"go.mau.fi/whatsmeow/types"
)

// jidToTypes converts a JID string to whatsmeow types.JID.
func jidToTypes(jid string) types.JID {
	parsed, _ := types.ParseJID(jid)
	return parsed
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	state := s.connMgr.State.Get()

	var msgCount, chatCount int
	s.db.Read.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount)
	s.db.Read.QueryRow("SELECT COUNT(*) FROM chats").Scan(&chatCount)

	resp := StatusResponse{
		State:        state.String(),
		IsConnected:  state == connection.Connected,
		Uptime:       time.Since(s.startedAt).Round(time.Second).String(),
		MessageCount: msgCount,
		ChatCount:    chatCount,
		StartedAt:    s.startedAt.Format(time.RFC3339),
	}

	if s.connMgr.Client != nil && s.connMgr.Client.Store != nil && s.connMgr.Client.Store.ID != nil {
		id := s.connMgr.Client.Store.ID
		phone := id.User
		resp.Identity = &IdentityInfo{
			JID:      id.String(),
			Phone:    phone,
			PushName: s.connMgr.Client.Store.PushName,
		}
	}

	writeJSON(w, 200, resp)
}

// Utility: check if jid is in the watched group list.
func (s *Server) isWatchedGroup(jid string) bool {
	for _, watched := range s.cfg.Bridge.Monitoring.WatchedGroupJIDs {
		if strings.TrimSpace(watched) == jid {
			return true
		}
	}
	return false
}
