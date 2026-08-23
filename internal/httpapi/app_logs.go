package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const logFetchCooldown = 30 * time.Second

// getAppLogs returns the most recently fetched runtime logs for an instance
// plus the state of the latest log fetch job. The caller must own the app.
func (s *Server) getAppLogs(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	var owned bool
	if err := s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM user_apps WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)", appID, p.ID).Scan(&owned); err != nil {
		s.internalError(w, err)
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	var logText string
	var sampledAt *time.Time
	if err := s.db.QueryRow(r.Context(), "SELECT log_text,sampled_at FROM app_runtime_logs WHERE user_app_id=$1", appID).Scan(&logText, &sampledAt); err != nil && err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var status, lastError string
	var jobUpdated *time.Time
	if err := s.db.QueryRow(r.Context(), `SELECT status,last_error,updated_at FROM app_log_fetch_jobs WHERE user_app_id=$1 ORDER BY requested_at DESC LIMIT 1`, appID).Scan(&status, &lastError, &jobUpdated); err != nil && err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"appId":     appID,
		"logs":      logText,
		"sampledAt": sampledAt,
		"status":    status,
		"lastError": lastError,
		"updatedAt": jobUpdated,
	})
}

// requestAppLogRefresh queues a fresh log fetch for the instance. Requests are
// throttled so a user cannot queue an unbounded number of Docker log reads.
func (s *Server) requestAppLogRefresh(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var exists bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM user_apps WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)", appID, p.ID).Scan(&exists); err != nil {
		s.internalError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	var pendingID string
	var pendingStatus string
	err = tx.QueryRow(r.Context(), `SELECT id,status FROM app_log_fetch_jobs
		WHERE user_app_id=$1 AND status IN ('queued','running')
		ORDER BY requested_at DESC LIMIT 1`, appID).Scan(&pendingID, &pendingStatus)
	if err == nil {
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusAccepted, map[string]any{"appId": appID, "status": pendingStatus, "idempotent": true})
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var recent bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_log_fetch_jobs
		WHERE user_app_id=$1 AND status='succeeded' AND completed_at>now()-make_interval(secs=>$2))`, appID, int(logFetchCooldown.Seconds())).Scan(&recent); err != nil {
		s.internalError(w, err)
		return
	}
	if recent {
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusAccepted, map[string]any{"appId": appID, "status": "cached", "cooldownSeconds": int(logFetchCooldown.Seconds())})
		return
	}
	var jobID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_log_fetch_jobs(user_app_id,requested_by) VALUES($1,$2) RETURNING id`, appID, p.ID).Scan(&jobID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"appId": appID, "jobId": jobID, "status": "queued"})
}
