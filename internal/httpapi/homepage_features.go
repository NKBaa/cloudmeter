package httpapi

import (
	"net/http"
	"strings"
	"time"
)

func (s *Server) getHomepage(w http.ResponseWriter, r *http.Request) {
	var html string
	var updatedAt time.Time
	if err := s.db.QueryRow(r.Context(), "SELECT content_html,updated_at FROM homepage_settings WHERE singleton").Scan(&html, &updatedAt); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contentHtml": html, "updatedAt": updatedAt})
}

func (s *Server) updateHomepage(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		ContentHTML string `json:"contentHtml"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.ContentHTML = strings.TrimSpace(q.ContentHTML)
	if q.ContentHTML == "" || len(q.ContentHTML) > 100000 {
		writeError(w, 400, "validation_failed", "homepage HTML must contain 1-100000 characters")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "UPDATE homepage_settings SET content_html=$1,updated_at=now(),updated_by=$2 WHERE singleton", q.ContentHTML, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'homepage.update','homepage_settings','singleton',$2,jsonb_build_object('content_length',$3::int))`, p.ID, requestID(r.Context()), len(q.ContentHTML)); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}
