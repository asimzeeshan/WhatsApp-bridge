package api

import (
	"net/http"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	q := store.LinkQuery{
		ChatJID:  stringParam(r, "chat_jid"),
		Platform: stringParam(r, "platform"),
		After:    int64Param(r, "after"),
		Before:   int64Param(r, "before"),
		Limit:    intParam(r, "limit", 50, 1, 500),
		Offset:   intParam(r, "page", 0, 0, 0) * intParam(r, "limit", 50, 1, 500),
	}

	links, total, err := s.db.QueryLinks(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if links == nil {
		links = []store.Link{}
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Data:   links,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	})
}
