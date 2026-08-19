package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var platformRestartOrder = []string{"egress-proxy", "app-router", "web", "api", "gateway"}

func processPlatformRestartOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id string
	var attempts int
	err = tx.QueryRow(ctx, `
		UPDATE platform_restart_requests request SET
		  status='running',attempts=attempts+1,last_error='',
		  started_at=coalesce(started_at,now()),updated_at=now()
		WHERE request.id=(
		  SELECT id FROM platform_restart_requests
		  WHERE status='queued' OR (status='running' AND updated_at<now()-interval '5 minutes')
		  ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING request.id::text,request.attempts`).Scan(&id, &attempts)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil || tx.Commit(ctx) != nil {
		return
	}
	if attempts > 2 {
		failPlatformRestart(ctx, db, id, "平台服务重启超过安全重试次数")
		return
	}
	if executor == nil {
		failPlatformRestart(ctx, db, id, "Docker 执行器不可用，未重启任何平台服务")
		return
	}

	for _, service := range platformRestartOrder {
		restartCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
		err = executor.RestartComposeService(restartCtx, service, true)
		cancel()
		if err != nil {
			message := fmt.Sprintf("重启 %s 失败：%v", service, err)
			failPlatformRestart(ctx, db, id, message)
			logger.Error("platform restart failed", "request_id", id, "service", service, "error", err)
			return
		}
		_, _ = db.Exec(ctx, `UPDATE platform_restart_requests SET updated_at=now() WHERE id=$1`, id)
	}

	// Mark the durable task complete before restarting this Worker. Docker will
	// terminate the process that sent the self-restart request, so there is no
	// reliable opportunity to write to PostgreSQL afterwards.
	if _, err = db.Exec(ctx, `UPDATE platform_restart_requests SET status='succeeded',completed_at=now(),updated_at=now(),last_error='' WHERE id=$1`, id); err != nil {
		logger.Error("platform restart completion update failed", "request_id", id, "error", err)
		return
	}
	logger.Info("platform services restarted; restarting worker", "request_id", id)
	selfCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = executor.RestartComposeService(selfCtx, "worker", false); err != nil {
		// A closed connection is expected when Docker stops this very process. If
		// the call returns an error, retain the completed task (other services did
		// restart) but emit a durable diagnostic for operators.
		logger.Warn("worker self-restart returned", "request_id", id, "error", err)
	}
}

func failPlatformRestart(ctx context.Context, db *pgxpool.Pool, id, message string) {
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, _ = db.Exec(ctx, `UPDATE platform_restart_requests SET status='failed',last_error=$2,completed_at=now(),updated_at=now() WHERE id=$1`, id, message)
}
