package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	q := store.ChatQuery{
		Query:      stringParam(r, "query"),
		Limit:      intParam(r, "limit", 100, 1, 500),
		Offset:     intParam(r, "page", 0, 0, 0) * intParam(r, "limit", 100, 1, 500),
		GroupsOnly: true,
	}

	groups, total, err := s.db.QueryChats(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if groups == nil {
		groups = []store.Chat{}
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Data:   groups,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	})
}

type groupDetailResponse struct {
	store.Chat
	Participants []participantInfo `json:"participants,omitempty"`
}

type participantInfo struct {
	JID          string `json:"jid"`
	IsAdmin      bool   `json:"is_admin"`
	IsSuperAdmin bool   `json:"is_super_admin"`
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")

	if !isGroupJID(jid) {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "jid must end with @g.us")
		return
	}

	// Get chat from DB
	chat, err := s.db.GetChat(jid)
	if err != nil {
		// Chat might not be in DB yet; try live fetch
		chat = &store.Chat{JID: jid, IsGroup: true}
	}

	resp := groupDetailResponse{Chat: *chat}

	// Fetch live participants from WhatsApp if connected
	if s.connMgr.State.IsConnected() && s.connMgr.Client != nil {
		info, err := s.connMgr.Client.GetGroupInfo(r.Context(), jidToTypes(jid))
		if err == nil {
			if info.Name != "" {
				resp.Name = info.Name
			}
			for _, p := range info.Participants {
				resp.Participants = append(resp.Participants, participantInfo{
					JID:          p.JID.String(),
					IsAdmin:      p.IsAdmin,
					IsSuperAdmin: p.IsSuperAdmin,
				})
			}
		} else {
			s.logger.Warn("failed to fetch group info from WA", "jid", jid, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
