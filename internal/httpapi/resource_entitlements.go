package httpapi

import (
	"context"

	runtimepolicy "cloudmeter/internal/runtime"
	"github.com/jackc/pgx/v5"
)

type resourceQuotaError struct {
	Code    string
	Message string
}

func (e resourceQuotaError) Error() string { return e.Message }

// enforceRuntimeEntitlements validates a selected instance shape. Resource
// capacity is billed per application and is no longer capped by a plan.
func enforceRuntimeEntitlements(ctx context.Context, tx pgx.Tx, userID, excludedAppID string, spec map[string]any) error {
	_, err := runtimepolicy.RuntimeResources(spec, false)
	if err != nil {
		return err
	}
	_, err = runtimepolicy.RuntimeStorage(spec, false)
	if err != nil {
		return err
	}
	_ = ctx
	_ = tx
	_ = userID
	_ = excludedAppID
	return nil
}
