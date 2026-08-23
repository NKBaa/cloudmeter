package httpapi

import "net/http"

func (s *Server) getLogRetentionSettings(w http.ResponseWriter, r *http.Request) {
	var hours int
	var bytes int64
	if err := s.db.QueryRow(r.Context(), "SELECT log_retention_hours,log_retention_bytes FROM system_state WHERE singleton").Scan(&hours, &bytes); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retentionHours": hours, "retentionBytes": bytes})
}

func (s *Server) updateLogRetentionSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		RetentionHours int   `json:"retentionHours"`
		RetentionBytes int64 `json:"retentionBytes"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.RetentionHours < 1 || q.RetentionHours > 8760 || q.RetentionBytes < 16384 || q.RetentionBytes > 1073741824 {
		writeError(w, 400, "validation_failed", "retention hours must be 1-8760 and retention bytes must be 16KiB-1GiB")
		return
	}
	if _, err := s.db.Exec(r.Context(), "UPDATE system_state SET log_retention_hours=$1,log_retention_bytes=$2 WHERE singleton", q.RetentionHours, q.RetentionBytes); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err := s.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'log-retention.settings.update','system_state','singleton',$2,jsonb_build_object('retention_hours',$3::int,'retention_bytes',$4::bigint))`, p.ID, requestID(r.Context()), q.RetentionHours, q.RetentionBytes); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retentionHours": q.RetentionHours, "retentionBytes": q.RetentionBytes})
}

func (s *Server) clearRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	if _, err := s.db.Exec(r.Context(), "DELETE FROM app_runtime_logs"); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err := s.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'log-retention.clear','app_runtime_logs','all',$2,jsonb_build_object())`, p.ID, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}
