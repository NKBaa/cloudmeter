package httpapi

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func enforceDeploymentConcurrency(ctx context.Context, tx pgx.Tx, userID string) error {
	// Deployments are metered operations. They are no longer gated by a
	// subscription entitlement; the job queue remains the concurrency guard.
	_ = ctx
	_ = tx
	_ = userID
	return nil
}
