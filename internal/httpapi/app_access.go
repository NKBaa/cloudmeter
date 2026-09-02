package httpapi

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// createAppAccess lets a browser navigation authenticate to the application
func (s *Server) createAppAccess(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var publicPath string
	if err := s.db.QueryRow(r.Context(), `SELECT route.public_path FROM user_apps app
		JOIN app_routes route ON route.user_app_id=app.id AND route.instance_id=app.instance_id
		WHERE app.id=$1 AND app.user_id=$2 AND app.deleted_at IS NULL AND app.status IN ('running','updating')`, appID, p.ID).Scan(&publicPath); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "route_not_found", "application route is not active")
			return
		}
		s.internalError(w, err)
		return
	}
	if !strings.HasPrefix(publicPath, "//") {
		writeError(w, http.StatusConflict, "invalid_route", "application wildcard route is not configured")
		return
	}
	var tlsEnabled bool
	if err := s.db.QueryRow(r.Context(), `SELECT tls_enabled FROM system_settings WHERE singleton`).Scan(&tlsEnabled); err != nil {
		s.internalError(w, err)
		return
	}
	if tlsEnabled {
		publicPath = "https:" + publicPath
	} else {
		publicPath = "http:" + publicPath
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"publicPath": publicPath})
}
