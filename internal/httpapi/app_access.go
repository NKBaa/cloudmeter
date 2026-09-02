package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const appAccessCookie = "cloudmeter_app_access"
const appAccessGrantTTL = 15 * time.Minute

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
	if strings.HasPrefix(publicPath, "//") {
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
		return
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	secure := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
	http.SetCookie(w, &http.Cookie{Name: appAccessCookie, Value: token, Path: "/apps/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(appAccessGrantTTL.Seconds()), Expires: time.Now().Add(appAccessGrantTTL)})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"publicPath": publicPath, "expiresInSeconds": int(appAccessGrantTTL.Seconds())})
}
