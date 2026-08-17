package httpapi

import (
	"context"
	"fmt"

	runtimepolicy "cloudmeter/internal/runtime"
	"github.com/jackc/pgx/v5"
)

type resourceQuotaError struct {
	Code    string
	Message string
}

func (e resourceQuotaError) Error() string { return e.Message }

// enforceRuntimeEntitlements reserves the target release against the user's
// subscription limits. The subscription row is locked by the caller's
// transaction, serializing concurrent app creation and updates for a user.
func enforceRuntimeEntitlements(ctx context.Context, tx pgx.Tx, userID, excludedAppID string, spec map[string]any) error {
	target, err := runtimepolicy.RuntimeResources(spec, false)
	if err != nil {
		return err
	}
	targetStorage, err := runtimepolicy.RuntimeStorage(spec, false)
	if err != nil {
		return err
	}
	var maxCPU, maxMemory, maxSystemDisk, maxDataDisk float64
	if err = tx.QueryRow(ctx, `SELECT coalesce((us.entitlements_snapshot->>'cpuCores')::numeric,0),coalesce((us.entitlements_snapshot->>'memoryGiB')::numeric,0),
		coalesce((us.entitlements_snapshot->>'systemDiskGiB')::numeric,0),coalesce((us.entitlements_snapshot->>'dataDiskGiB')::numeric,0)
        FROM user_subscriptions us
        WHERE us.user_id=$1 AND (us.status='grace_period' OR (us.status='active' AND (us.ends_at IS NULL OR us.ends_at+interval '3 days'>now())))
		FOR UPDATE OF us`, userID).Scan(&maxCPU, &maxMemory, &maxSystemDisk, &maxDataDisk); err != nil {
		if err == pgx.ErrNoRows {
			return resourceQuotaError{Code: "subscription_required", Message: "an active subscription is required"}
		}
		return err
	}
	var usedCPU, usedMemory, usedSystemDisk, usedDataDisk float64
	err = tx.QueryRow(ctx, `SELECT
		coalesce(sum(CASE WHEN a.status IN ('deploying','running','updating') THEN coalesce((r.immutable_snapshot->'runtime_spec'->>'cpuCores')::numeric,1) ELSE 0 END),0),
		coalesce(sum(CASE WHEN a.status IN ('deploying','running','updating') THEN coalesce((r.immutable_snapshot->'runtime_spec'->>'memoryMiB')::numeric,512) ELSE 0 END)/1024,0),
		coalesce(sum(CASE WHEN a.status IN ('deploying','running','updating') THEN coalesce((r.immutable_snapshot->'runtime_spec'->>'systemDiskGiB')::numeric,5) ELSE 0 END),0),
		coalesce(sum((SELECT coalesce(sum(coalesce((volume->>'sizeGiB')::numeric,10)),0)
		  FROM jsonb_array_elements(coalesce(r.immutable_snapshot->'runtime_spec'->'volumes','[]'::jsonb)) volume)),0)
		FROM user_apps a JOIN LATERAL (
		  SELECT immutable_snapshot FROM app_releases candidate
		  WHERE candidate.user_app_id=a.id AND candidate.state<>'failed'
		  ORDER BY candidate.release_number DESC LIMIT 1
		) r ON true
		WHERE a.user_id=$1 AND ($2::text='' OR a.id::text<>$2)
		  AND a.status IN ('stopped','stopping','deploying','running','updating','suspended')`, userID, excludedAppID).Scan(&usedCPU, &usedMemory, &usedSystemDisk, &usedDataDisk)
	if err != nil {
		return err
	}
	if usedCPU+target.CPUCores > maxCPU+1e-9 {
		return resourceQuotaError{Code: "cpu_quota_exceeded", Message: fmt.Sprintf("CPU entitlement exceeded: %.2f of %.2f cores reserved", usedCPU+target.CPUCores, maxCPU)}
	}
	if usedMemory+target.MemoryMiB/1024 > maxMemory+1e-9 {
		return resourceQuotaError{Code: "memory_quota_exceeded", Message: fmt.Sprintf("memory entitlement exceeded: %.2f of %.2f GiB reserved", usedMemory+target.MemoryMiB/1024, maxMemory)}
	}
	if usedSystemDisk+targetStorage.SystemDiskGiB > maxSystemDisk+1e-9 {
		return resourceQuotaError{Code: "system_disk_quota_exceeded", Message: fmt.Sprintf("system disk entitlement exceeded: %.2f of %.2f GiB reserved", usedSystemDisk+targetStorage.SystemDiskGiB, maxSystemDisk)}
	}
	if usedDataDisk+targetStorage.DataDiskGiB > maxDataDisk+1e-9 {
		return resourceQuotaError{Code: "data_disk_quota_exceeded", Message: fmt.Sprintf("data disk entitlement exceeded: %.2f of %.2f GiB reserved", usedDataDisk+targetStorage.DataDiskGiB, maxDataDisk)}
	}
	return nil
}
