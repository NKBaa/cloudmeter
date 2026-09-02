package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// processLogFetchOne claims a pending instance log fetch job, reads the
// container diagnostics (state plus recent stdout/stderr), and stores the
// result so the API can serve it without direct Docker access. Follows the
// same retry/backoff shape as the application stop jobs.
func processLogFetchOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, appID string
	var attempts int
	err = tx.QueryRow(ctx, `UPDATE app_log_fetch_jobs job
		SET status='running',attempts=attempts+1,last_error='',updated_at=now()
		WHERE job.id=(
		  SELECT id FROM app_log_fetch_jobs
		  WHERE available_at<=now()
		    AND (status='queued' OR (status='running' AND updated_at<now()-interval '1 minute'))
		  ORDER BY requested_at FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING job.id,job.user_app_id,job.attempts`).Scan(&id, &appID, &attempts)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("app log fetch claim failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("app log fetch claim commit failed", "error", err)
		return
	}
	if executor == nil {
		markLogFetchRetry(ctx, db, id, attempts, fmt.Errorf("docker executor is unavailable"), logger)
		return
	}
	var container string
	_ = db.QueryRow(ctx, "SELECT coalesce(route.upstream_container,'') FROM app_routes route WHERE route.user_app_id=$1", appID).Scan(&container)
	if container == "" {
		markLogFetchRetry(ctx, db, id, attempts, fmt.Errorf("application has no running container"), logger)
		return
	}
	logs, err := executor.ContainerDiagnostics(ctx, container, 300)
	if err != nil {
		markLogFetchRetry(ctx, db, id, attempts, err, logger)
		return
	}
	if _, err = db.Exec(ctx, `INSERT INTO app_runtime_logs(user_app_id,log_text,sampled_at,updated_at)
		VALUES($1,$2,now(),now())
		ON CONFLICT(user_app_id) DO UPDATE SET log_text=EXCLUDED.log_text,sampled_at=EXCLUDED.sampled_at,updated_at=now()`, appID, logs); err != nil {
		markLogFetchRetry(ctx, db, id, attempts, err, logger)
		return
	}
	if _, err = db.Exec(ctx, `UPDATE app_log_fetch_jobs SET status='succeeded',completed_at=now(),updated_at=now() WHERE id=$1 AND status='running'`, id); err != nil {
		logger.Error("app log fetch completion update failed", "job", id, "error", err)
		return
	}
	logger.Info("app logs fetched", "job", id, "app", appID)
}

func markLogFetchRetry(ctx context.Context, db *pgxpool.Pool, id string, attempts int, cause error, logger *slog.Logger) {
	shift := attempts
	if shift > 6 {
		shift = 6
	}
	backoff := time.Duration(1<<shift) * time.Second
	if _, err := db.Exec(ctx, `UPDATE app_log_fetch_jobs SET status='queued',last_error=$2,available_at=$3,updated_at=now() WHERE id=$1 AND status='running'`, id, cause.Error(), time.Now().Add(backoff)); err != nil {
		logger.Error("app log fetch retry update failed", "job", id, "error", err)
	}
	logger.Warn("app log fetch will retry", "job", id, "error", cause)
}

// pruneRuntimeLogs applies the global runtime-log retention policy: drop rows
// not sampled within the retention window and truncate any single log payload
// beyond the configured byte budget, keeping the most recent tail.
func pruneRuntimeLogs(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	var hours int
	var maxBytes int64
	if err := db.QueryRow(ctx, "SELECT log_retention_hours,log_retention_bytes FROM system_state WHERE singleton").Scan(&hours, &maxBytes); err != nil {
		logger.Error("log retention settings lookup failed", "error", err)
		return
	}
	if _, err := db.Exec(ctx, `DELETE FROM app_runtime_logs WHERE sampled_at < now() - make_interval(hours => $1)`, hours); err != nil {
		logger.Error("runtime log age prune failed", "error", err)
	}
	if _, err := db.Exec(ctx, `UPDATE app_runtime_logs SET log_text = right(log_text, $1), updated_at=now() WHERE octet_length(log_text) > $1`, maxBytes); err != nil {
		logger.Error("runtime log size prune failed", "error", err)
	}
}
