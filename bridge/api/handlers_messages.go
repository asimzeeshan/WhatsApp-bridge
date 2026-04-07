package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	q := store.MessageQuery{
		ChatJID: stringParam(r, "chat_jid"),
		Sender:  stringParam(r, "sender"),
		After:   int64Param(r, "after"),
		Before:  int64Param(r, "before"),
		Query:   stringParam(r, "query"),
		Limit:   intParam(r, "limit", 50, 1, 500),
		Offset:  intParam(r, "page", 0, 0, 0) * intParam(r, "limit", 50, 1, 500),
	}

	msgs, total, err := s.db.QueryMessages(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if msgs == nil {
		msgs = []store.Message{}
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Data:   msgs,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	})
}

func (s *Server) handleMessageContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	chatJID := stringParam(r, "chat_jid")
	ctx := intParam(r, "context", 5, 1, 50)

	if chatJID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "chat_jid is required")
		return
	}

	msgs, err := s.db.GetMessageContext(id, chatJID, ctx)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleCheckNewMessages(w http.ResponseWriter, r *http.Request) {
	jid := stringParam(r, "jid")
	if jid == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "jid is required")
		return
	}
	if !isValidJID(jid) {
		writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid jid format")
		return
	}

	limit := intParam(r, "limit", 100, 1, 500)

	msgs, err := s.db.CheckNewMessages(jid, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if msgs == nil {
		msgs = []store.Message{}
	}

	writeJSON(w, http.StatusOK, CheckResponse{
		Messages: msgs,
		Count:    len(msgs),
		JID:      jid,
	})
}

func (s *Server) handleCheckTriggers(w http.ResponseWriter, r *http.Request) {
	var req TriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if len(req.JIDs) == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "jids is required and must not be empty")
		return
	}

	// Validate all JIDs
	for _, jid := range req.JIDs {
		if !isValidJID(jid) {
			writeError(w, http.StatusBadRequest, "INVALID_JID", "invalid jid: "+jid)
			return
		}
	}

	if req.Limit <= 0 {
		req.Limit = 100
	}

	filters := store.TriggerFilters{
		MentionJID: req.Filters.MentionJID,
		SenderJIDs: req.Filters.SenderJIDs,
	}

	result, err := s.db.CheckTriggersMulti(req.JIDs, filters, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Convert store response to API response
	groups := make(map[string]TriggerGroupResult)
	for jid, gr := range result.Groups {
		msgs := gr.Messages
		if msgs == nil {
			msgs = []store.Message{}
		}
		groups[jid] = TriggerGroupResult{
			Count:    gr.Count,
			Messages: msgs,
		}
	}

	writeJSON(w, http.StatusOK, TriggerResponse{
		Total:  result.Total,
		Groups: groups,
	})
}
