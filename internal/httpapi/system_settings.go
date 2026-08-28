package httpapi

import (
	"net/http"
	"strings"
	"time"
)

type SystemSettingsResponse struct {
	SystemName string    `json:"systemName"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (s *Server) getSystemSettingsPublic(w http.ResponseWriter, r *http.Request) {
	var systemName string
	var updatedAt time.Time
	err := s.db.QueryRow(r.Context(), "SELECT system_name, updated_at FROM system_settings WHERE singleton").Scan(&systemName, &updatedAt)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"systemName": "CloudMeter"})
		return
	}
	if systemName == "" {
		systemName = "CloudMeter"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"systemName": systemName,
		"updatedAt":  updatedAt,
	})
}

func (s *Server) getSystemSettings(w http.ResponseWriter, r *http.Request) {
	var systemName string
	var updatedAt time.Time
	err := s.db.QueryRow(r.Context(), "SELECT system_name, updated_at FROM system_settings WHERE singleton").Scan(&systemName, &updatedAt)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if systemName == "" {
		systemName = "CloudMeter"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"systemName": systemName,
		"updatedAt":  updatedAt,
	})
}

func (s *Server) updateSystemSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		SystemName string `json:"systemName"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.SystemName = strings.TrimSpace(q.SystemName)
	if q.SystemName == "" || len([]rune(q.SystemName)) > 64 {
		writeError(w, 400, "validation_failed", "系统名称必须在 1 到 64 个字符之间")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "INSERT INTO system_settings(singleton, system_name, updated_at, updated_by) VALUES (true, $1, now(), $2) ON CONFLICT (singleton) DO UPDATE SET system_name=$1, updated_at=now(), updated_by=$2", q.SystemName, p.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'system.settings.update','system_settings','singleton',$2,jsonb_build_object('system_name',$3::text))`, p.ID, requestID(r.Context()), q.SystemName); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "systemName": q.SystemName})
}
