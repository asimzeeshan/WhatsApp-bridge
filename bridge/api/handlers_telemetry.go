package api

import (
	"encoding/json"
	"net/http"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

func (s *Server) handleTelemetryDaily(w http.ResponseWriter, r *http.Request) {
	date := stringParam(r, "date")
	t, err := s.db.GetDailyTelemetry(date)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTelemetryTools(w http.ResponseWriter, r *http.Request) {
	q := store.ToolCallQuery{
		ToolName: stringParam(r, "tool_name"),
		Limit:    intParam(r, "limit", 50, 1, 500),
		Offset:   intParam(r, "page", 0, 0, 0) * intParam(r, "limit", 50, 1, 500),
	}

	calls, total, err := s.db.QueryToolCalls(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if calls == nil {
		calls = []store.ToolCall{}
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Data:   calls,
		Total:  total,
		Limit:  q.Limit,
		Offset: q.Offset,
	})
}

func (s *Server) handleRecordToolCall(w http.ResponseWriter, r *http.Request) {
	var req ToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if req.ToolName == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELD", "tool_name is required")
		return
	}

	s.db.InsertToolCall(&store.ToolCall{
		ToolName:   req.ToolName,
		DurationMs: req.DurationMs,
		Success:    req.Success,
		ErrorMsg:   req.ErrorMsg,
	})

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
