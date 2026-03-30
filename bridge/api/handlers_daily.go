package api

import "net/http"

func (s *Server) handleDailySummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "not_implemented",
		"message": "Daily summaries will be available in a future phase using LLM summarization.",
	})
}
