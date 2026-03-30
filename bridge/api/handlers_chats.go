package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	q := store.ChatQuery{
		Query:  stringParam(r, "query"),
		Limit:  intParam(r, "limit", 100, 1, 500),
		Offset: intParam(r, "page", 0, 0, 0) * intParam(r, "limit", 100, 1, 500),
	}

	chats, total, err := s.db.QueryChats(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if chats == nil {
		chats = []store.Chat{}
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Data:   chats,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	})
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")

	chat, err := s.db.GetChat(jid)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "chat not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, chat)
}

func (s *Server) handleUnread(w http.ResponseWriter, r *http.Request) {
	if boolParam(r, "flat") {
		msgs, err := s.db.GetFlatUnreadMessages()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}
		if msgs == nil {
			msgs = []store.FlatUnreadMessage{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"totalUnread": len(msgs),
			"messages":    msgs,
		})
		return
	}

	msgLimit := intParam(r, "msg_limit", 5, 1, 50)
	chats, err := s.db.GetUnreadChats(msgLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if chats == nil {
		chats = []store.UnreadChat{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chats": chats,
		"total": len(chats),
	})
}
