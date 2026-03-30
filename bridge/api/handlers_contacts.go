package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := s.db.QueryContacts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, contacts)
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")

	contact, err := s.db.GetContact(jid)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "contact not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, contact)
}
