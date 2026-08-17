package httpapi

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func enforceDeploymentConcurrency(ctx context.Context, tx pgx.Tx, userID string) error {
	var limit, active int
	err := tx.QueryRow(ctx, `SELECT coalesce((us.entitlements_snapshot->>'concurrentDeployments')::int,0),
		(SELECT count(*) FROM deployment_jobs j JOIN user_apps a ON a.id=j.user_app_id
		 WHERE a.user_id=$1 AND j.state NOT IN ('succeeded','failed'))
		FROM user_subscriptions us WHERE us.user_id=$1
		AND (us.status='grace_period' OR (us.status='active' AND (us.ends_at IS NULL OR us.ends_at+interval '3 days'>now())))
		FOR UPDATE`, userID).Scan(&limit, &active)
	if err == pgx.ErrNoRows {
		return resourceQuotaError{Code: "subscription_required", Message: "an active subscription is required"}
	}
	if err != nil {
		return err
	}
	if active >= limit {
		return resourceQuotaError{Code: "deployment_concurrency_exceeded", Message: "concurrent deployment entitlement has been reached"}
	}
	return nil
}
