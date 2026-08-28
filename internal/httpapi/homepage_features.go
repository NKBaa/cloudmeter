package httpapi

import (
	"net/http"
)

func (s *Server) getHomepage(w http.ResponseWriter, r *http.Request) {
	var contentHTML string
	err := s.db.QueryRow(r.Context(), "SELECT homepage_content FROM system_settings WHERE singleton").Scan(&contentHTML)
	if err != nil {
		contentHTML = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"contentHtml": contentHTML})
}
