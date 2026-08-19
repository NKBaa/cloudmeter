package httpapi

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type platformRestartRequest struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"lastError"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
}

func scanPlatformRestart(row pgx.Row) (platformRestartRequest, error) {
	var request platformRestartRequest
	err := row.Scan(
		&request.ID, &request.Status, &request.Attempts, &request.LastError,
		&request.CreatedAt, &request.StartedAt, &request.CompletedAt,
	)
	return request, err
}

func (s *Server) getPlatformRestart(w http.ResponseWriter, r *http.Request) {
	request, err := scanPlatformRestart(s.db.QueryRow(r.Context(), `
		SELECT id::text,status,attempts,last_error,created_at,started_at,completed_at
		FROM platform_restart_requests ORDER BY created_at DESC LIMIT 1`))
	if err == pgx.ErrNoRows {
		writeJSON(w, http.StatusOK, map[string]any{"request": nil})
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": request})
}

func (s *Server) createPlatformRestart(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	// The advisory lock makes the active-request check deterministic even if
	// two administrators click restart at the same instant.
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock($1)`, int64(729104509)); err != nil {
		s.internalError(w, err)
		return
	}
	var activeID string
	err = tx.QueryRow(r.Context(), `
		SELECT id::text FROM platform_restart_requests
		WHERE status IN ('queued','running') ORDER BY created_at LIMIT 1`).Scan(&activeID)
	if err == nil {
		writeError(w, http.StatusConflict, "restart_in_progress", "a platform restart is already in progress")
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}

	request, err := scanPlatformRestart(tx.QueryRow(r.Context(), `
		INSERT INTO platform_restart_requests(requested_by) VALUES($1)
		RETURNING id::text,status,attempts,last_error,created_at,started_at,completed_at`, p.auditActorID()))
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `
		INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'system.restart.request','platform_restart_request',$2,$3,
		jsonb_build_object('scope','platform_services','excludes',ARRAY['postgres','redis','docker-engine','user-apps']))`,
		p.auditActorID(), request.ID, requestID(r.Context())); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"request": request})
}
