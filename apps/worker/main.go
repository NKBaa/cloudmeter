package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloudmeter/internal/config"
	"cloudmeter/internal/domain"
	runtimepolicy "cloudmeter/internal/runtime"
	"cloudmeter/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var executor *runtimepolicy.DockerExecutor
var routerContainer string
var backupVolume string
var backupHelperImage string
var secrets *secretbox.Box
var egressProxyContainer string
var egressToken string
var runtimeScope string
var runtimeOwner string

const deploymentMaxHealthAttempts = 8

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	routerContainer = cfg.RouterContainer
	runtimeOwner = cfg.RuntimeOwner
	backupVolume = runtimepolicy.BackupVolumeName(runtimeOwner, cfg.BackupVolume)
	backupHelperImage = cfg.BackupHelperImage
	egressProxyContainer = cfg.EgressProxy
	egressToken = cfg.EgressIngestToken
	runtimeScope = runtimepolicy.ResourceScopeToken(runtimeOwner)
	secrets, err = secretbox.New(cfg.SecretsKey)
	if err != nil {
		logger.Error("secret encryption configuration failed", "error", err)
		os.Exit(1)
	}
	if cfg.DockerExecutor {
		executor = runtimepolicy.NewDockerExecutor(cfg.DockerSocket, cfg.RuntimeOwner)
		if pingErr := executor.Ping(ctx); pingErr != nil {
			logger.Error("docker executor unavailable", "socket", cfg.DockerSocket, "error", pingErr)
			executor = nil
		} else {
			logger.Info("docker executor enabled", "socket", cfg.DockerSocket)
		}
	}
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	syncDockerDaemonSettings(ctx, db)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	logger.Info("worker started")
	lastDockerSettingsSync := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(lastDockerSettingsSync) >= 30*time.Second {
				syncDockerDaemonSettings(ctx, db)
				lastDockerSettingsSync = time.Now()
			}
			processPlatformRestartOne(ctx, db, logger)
			processAppDeletionOne(ctx, db, logger)
			reconcileRouterNetworks(ctx, db, logger)
			reconcileEgressNetworks(ctx, db, logger)
			processStopOne(ctx, db, logger)
			reconcileRuntimeContainers(ctx, db, logger)
			reconcileProductTestContainers(ctx, db, logger)
			processBackupDeletionOne(ctx, db, logger)
			processBackupOne(ctx, db, logger)
			processRestoreOne(ctx, db, logger)
			processOne(ctx, db, logger)
			processProductVersionTestOne(ctx, db, logger)
			meterRuntime(ctx, db, logger)
			meterStorage(ctx, db, logger)
			meterEgress(ctx, db, logger)
			aggregateUsage(ctx, db, logger)
			sealUnpricedUsage(ctx, db, logger)
			billUsage(ctx, db, logger)
		}
	}
}

func processAppDeletionOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, appID string
	var attempts int
	err = tx.QueryRow(ctx, `UPDATE app_deletion_jobs SET status='running',attempts=attempts+1,updated_at=now(),last_error=NULL WHERE id=(SELECT id FROM app_deletion_jobs WHERE available_at<=now() AND (status='queued' OR (status='running' AND updated_at<now()-interval '1 minute')) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,user_app_id,attempts`).Scan(&id, &appID, &attempts)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		return
	}
	if tx.Commit(ctx) != nil {
		return
	}
	if executor == nil {
		_, _ = db.Exec(ctx, `UPDATE app_deletion_jobs SET status='queued',last_error='docker unavailable',available_at=now()+interval '10 seconds' WHERE id=$1`, id)
		return
	}
	prefixes := []string{"cm-" + appID + "-"}
	if runtimeScope != "" {
		prefixes = append(prefixes, "cm-"+runtimeScope+"-"+appID+"-")
	}
	for _, prefix := range prefixes {
		names, _ := executor.ContainerNames(ctx, prefix)
		for _, name := range names {
			_ = executor.RemoveIfExists(ctx, name)
		}
	}
	rows, _ := db.Query(ctx, `SELECT DISTINCT volume->>'name' FROM app_releases r CROSS JOIN LATERAL jsonb_array_elements(coalesce(r.immutable_snapshot->'runtime_spec'->'volumes','[]'::jsonb)) volume WHERE r.user_app_id=$1`, appID)
	if rows != nil {
		for rows.Next() {
			var key string
			_ = rows.Scan(&key)
			_ = executor.RemoveVolumeIfExists(ctx, runtimepolicy.AppVolumeNameForOwner(runtimeOwner, appID, key))
		}
		rows.Close()
	}
	_, _ = db.Exec(ctx, `UPDATE app_deletion_jobs SET status='succeeded',completed_at=now(),updated_at=now() WHERE id=$1`, id)
}

func processStopOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)

	var id, appID, releaseID, container string
	var attempts int
	err = tx.QueryRow(ctx, `UPDATE app_stop_jobs job
		SET status='running',attempts=attempts+1,last_error=NULL,updated_at=now()
		WHERE job.id=(
		  SELECT id FROM app_stop_jobs
		  WHERE available_at<=now()
		    AND (status='queued' OR (status='running' AND updated_at<now()-interval '1 minute'))
		  ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING job.id,job.user_app_id,coalesce(job.release_id::text,''),job.container_name,job.attempts`).Scan(&id, &appID, &releaseID, &container, &attempts)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("app stop claim failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("app stop claim commit failed", "error", err)
		return
	}

	if container != "" {
		if !stopContainerMatches(appID, releaseID, container) {
			err = fmt.Errorf("stop job container identity is invalid")
		} else if executor == nil {
			err = fmt.Errorf("docker executor is unavailable")
		} else {
			err = executor.StopIfExists(ctx, container)
			if err == nil {
				err = executor.RemoveIfExists(ctx, container)
			}
		}
	}
	if err != nil {
		shift := attempts
		if shift > 6 {
			shift = 6
		}
		backoff := time.Duration(1<<shift) * time.Second
		if _, updateErr := db.Exec(ctx, `UPDATE app_stop_jobs SET status='queued',last_error=$2,available_at=$3,updated_at=now()
			WHERE id=$1 AND status='running'`, id, err.Error(), time.Now().Add(backoff)); updateErr != nil {
			logger.Error("app stop retry update failed", "job", id, "error", updateErr)
		}
		logger.Warn("app stop will retry", "job", id, "container", container, "error", err)
		return
	}

	tx, err = db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE app_stop_jobs SET status='succeeded',last_error=NULL,completed_at=now(),updated_at=now()
		WHERE id=$1 AND status='running'`, id); err != nil {
		logger.Error("app stop completion update failed", "job", id, "error", err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE user_apps SET status='stopped',suspension_reason=NULL
		WHERE id=$1 AND status='stopping'`, appID); err != nil {
		logger.Error("app stopped state update failed", "job", id, "error", err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(subject_user_id,action,resource_type,resource_id,request_id,metadata)
		SELECT user_id,'app.stop.complete','user_app',id::text,'worker/app-stop/'||$2::text,
		jsonb_build_object('stop_job_id',$2::text,'container',$3::text) FROM user_apps WHERE id=$1`, appID, id, container); err != nil {
		logger.Error("app stop completion audit failed", "job", id, "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("app stop completion commit failed", "job", id, "error", err)
		return
	}
	logger.Info("application stopped", "job", id, "app", appID)
}

// Reconcile the current month's subscription allowance. The business reference
// is unique per user and UTC month, so retries and worker restarts are harmless.
func processSubscriptionCredits(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	_, err := db.Exec(ctx, `WITH eligible AS MATERIALIZED (
			SELECT us.user_id,(us.entitlements_snapshot->>'creditGrantCents')::bigint AS target_cents
			FROM user_subscriptions us
			JOIN users u ON u.id=us.user_id AND u.status='active'
			WHERE us.starts_at < (date_trunc('month',now() AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC'
			  AND ((us.status='active' AND (us.ends_at IS NULL OR us.ends_at>now()))
			       OR (us.status='grace_period' AND us.grace_ends_at IS NOT NULL AND us.grace_ends_at>now()))
			  AND coalesce((us.entitlements_snapshot->>'creditGrantCents')::bigint,0)>0
			  AND coalesce((
				SELECT sum(g.amount_cents) FROM credit_grants g
				WHERE g.user_id=us.user_id
				  AND (g.business_ref='subscription-credit/' || us.user_id::text || '/' || to_char(now() AT TIME ZONE 'UTC','YYYY-MM')
				       OR g.business_ref LIKE 'subscription-credit/' || us.user_id::text || '/' || to_char(now() AT TIME ZONE 'UTC','YYYY-MM') || '/%')
			  ),0)<(us.entitlements_snapshot->>'creditGrantCents')::bigint
			ORDER BY us.user_id
			LIMIT 500
		), new_grants AS (
			SELECT grant_row.id,eligible.user_id,grant_row.amount_cents,grant_row.business_ref
			FROM eligible
			CROSS JOIN LATERAL grant_subscription_credit(eligible.user_id,NULL::uuid) grant_row
		)
		INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		SELECT NULL,user_id,'subscription.credit_grant','credit_grant',id::text,
		       'worker/' || business_ref,
		       jsonb_build_object('amount_cents',amount_cents,'business_ref',business_ref,'source','worker_reconciliation')
		FROM new_grants`)
	if err != nil {
		logger.Error("subscription credit reconciliation failed", "error", err)
	}
}

var runtimeContainerPattern = regexp.MustCompile(`^cm-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
var scopedRuntimeContainerPattern = regexp.MustCompile(`^cm-([0-9a-f]{10})-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

func runtimeContainerIdentity(name string) (appID, releaseID string, legacy, ok bool) {
	if match := scopedRuntimeContainerPattern.FindStringSubmatch(name); match != nil {
		if runtimeScope == "" || match[1] != runtimeScope {
			return "", "", false, false
		}
		return match[2], match[3], false, true
	}
	if match := runtimeContainerPattern.FindStringSubmatch(name); match != nil {
		return match[1], match[2], true, true
	}
	return "", "", false, false
}

func stopContainerMatches(appID, releaseID, name string) bool {
	containerAppID, containerReleaseID, legacy, ok := runtimeContainerIdentity(name)
	if !ok || containerAppID != appID || containerReleaseID != releaseID {
		return false
	}
	return !legacy || runtimepolicy.UsesLegacyResourceNames(runtimeOwner)
}

func reconcileRuntimeContainers(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	if executor == nil {
		return
	}
	names, err := executor.ContainerNames(ctx, "cm-")
	if err != nil {
		logger.Warn("runtime container reconciliation failed", "error", err)
		return
	}
	for _, name := range names {
		appID, releaseID, legacy, ok := runtimeContainerIdentity(name)
		if !ok {
			continue
		}
		var appExists, keep bool
		err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_apps WHERE id=$1), EXISTS(
			SELECT 1 FROM user_apps app
			LEFT JOIN app_routes route ON route.user_app_id=app.id
			WHERE app.id=$1 AND (
				(app.status IN ('deploying','running','updating') AND (
					route.upstream_container=$3 OR EXISTS(SELECT 1 FROM deployment_jobs job WHERE job.user_app_id=app.id AND job.release_id=$2 AND job.state NOT IN ('succeeded','failed'))
				)) OR (app.status='stopping' AND EXISTS(
					SELECT 1 FROM app_stop_jobs stop_job WHERE stop_job.user_app_id=app.id
					  AND stop_job.container_name=$3 AND stop_job.status IN ('queued','running')
				))
			)
		)`, appID, releaseID, name).Scan(&appExists, &keep)
		if err != nil {
			logger.Warn("runtime container ownership query failed", "container", name, "error", err)
			continue
		}
		if keep {
			continue
		}
		// Unlabelled legacy names can be shared by an older Compose stack on
		// the same Engine. Only reclaim one when this database owns the app.
		if legacy && !appExists {
			continue
		}
		if err = executor.Remove(ctx, name); err != nil {
			logger.Warn("orphan runtime container cleanup failed", "container", name, "error", err)
		} else {
			logger.Info("orphan runtime container removed", "container", name)
		}
	}
}

func processExpiredSubscriptions(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		logger.Error("subscription expiry transaction failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	reminderResult, err := tx.Exec(ctx, `INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata)
		SELECT user_id,'subscription-expiring/'||extract(epoch FROM ends_at)::bigint,'subscription_expiring','warning',
		       '套餐即将到期','当前套餐将在 3 天内到期，平台不会自动续费，请按需手动续期。',
		       jsonb_build_object('ends_at',ends_at)
		FROM user_subscriptions
		WHERE status='active' AND ends_at>now() AND ends_at<=now()+interval '3 days'
		ON CONFLICT(user_id,event_key) DO NOTHING`)
	if err != nil {
		logger.Error("subscription expiry reminder failed", "error", err)
		return
	}
	graceResult, err := tx.Exec(ctx, `WITH transitioned AS (
		UPDATE user_subscriptions SET status='grace_period',grace_ends_at=ends_at+interval '3 days',updated_at=now()
		WHERE status='active' AND ends_at IS NOT NULL AND ends_at<=now()
		RETURNING user_id,ends_at,grace_ends_at
	), notified AS (
		INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata)
		SELECT user_id,'subscription-grace/'||extract(epoch FROM ends_at)::bigint,'subscription_grace','warning',
		       '套餐已进入宽限期','套餐已经到期，应用将在宽限期结束后暂停。平台不会自动续费。',
		       jsonb_build_object('ends_at',ends_at,'grace_ends_at',grace_ends_at)
		FROM transitioned ON CONFLICT(user_id,event_key) DO NOTHING
	)
	INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
	SELECT NULL,user_id,'subscription.grace_period','user_subscription',user_id::text,
	       'worker/subscription-grace/'||extract(epoch FROM ends_at)::bigint,
	       jsonb_build_object('ends_at',ends_at,'grace_ends_at',grace_ends_at)
	FROM transitioned`)
	if err != nil {
		logger.Error("subscription grace period update failed", "error", err)
		return
	}
	rows, err := tx.Query(ctx, `WITH transitioned AS (
		UPDATE user_subscriptions SET status='expired',updated_at=now()
		WHERE status='grace_period' AND grace_ends_at IS NOT NULL AND grace_ends_at<=now()
		RETURNING user_id,grace_ends_at
	), notified AS (
		INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata)
		SELECT user_id,'subscription-expired/'||extract(epoch FROM grace_ends_at)::bigint,'subscription_expired','critical',
		       '套餐已过期，应用已暂停','宽限期已经结束。购买套餐后，平台会通过正常部署流程恢复可恢复的应用。',
		       jsonb_build_object('grace_ends_at',grace_ends_at)
		FROM transitioned ON CONFLICT(user_id,event_key) DO NOTHING
	), audited AS (
		INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		SELECT NULL,user_id,'subscription.expire','user_subscription',user_id::text,
		       'worker/subscription-expire/'||extract(epoch FROM grace_ends_at)::bigint,
		       jsonb_build_object('grace_ends_at',grace_ends_at) FROM transitioned
	)
	SELECT user_id FROM transitioned`)
	if err != nil {
		logger.Error("subscription expiry update failed", "error", err)
		return
	}
	var users []string
	for rows.Next() {
		var userID string
		if rows.Scan(&userID) == nil {
			users = append(users, userID)
		}
	}
	rows.Close()
	var containers []string
	for _, userID := range users {
		containerRows, queryErr := tx.Query(ctx, `SELECT upstream_container FROM app_routes WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id=$1) AND upstream_container<>''`, userID)
		if queryErr == nil {
			for containerRows.Next() {
				var name string
				if containerRows.Scan(&name) == nil {
					containers = append(containers, name)
				}
			}
			containerRows.Close()
		}
		if _, err = tx.Exec(ctx, `UPDATE user_apps SET status='suspended',suspension_reason='subscription_expired' WHERE user_id=$1 AND status IN ('running','updating','deploying')`, userID); err != nil {
			logger.Error("subscription app suspension failed", "user", userID, "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE deployment_jobs SET state='failed',last_error='subscription expired',updated_at=now() WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id=$1) AND state NOT IN ('succeeded','failed')`, userID); err != nil {
			logger.Error("expired deployment cancellation failed", "user", userID, "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE app_restore_jobs SET status='failed',last_error='subscription expired',completed_at=now() WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id=$1) AND status='queued'`, userID); err != nil {
			logger.Error("expired restore cancellation failed", "user", userID, "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `DELETE FROM app_routes WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id=$1)`, userID); err != nil {
			logger.Error("subscription route close failed", "user", userID, "error", err)
			return
		}
	}
	if len(users) > 0 || graceResult.RowsAffected() > 0 || reminderResult.RowsAffected() > 0 {
		if err = tx.Commit(ctx); err != nil {
			logger.Error("subscription expiry commit failed", "error", err)
			return
		}
		if executor != nil {
			for _, name := range containers {
				if stopErr := executor.Stop(ctx, name); stopErr != nil {
					logger.Warn("expired app stop failed", "container", name, "error", stopErr)
				}
				if removeErr := executor.Remove(ctx, name); removeErr != nil {
					logger.Warn("expired app cleanup failed", "container", name, "error", removeErr)
				}
			}
		}
		if graceResult.RowsAffected() > 0 {
			logger.Info("subscriptions entered grace period", "subscriptions", graceResult.RowsAffected())
		}
		if len(users) > 0 {
			logger.Warn("expired subscriptions enforced", "users", len(users))
		}
	}
}

func processBackupOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, appID, volumeKey, sourceVolume, storageKey, userID string
	err = tx.QueryRow(ctx, `UPDATE app_backups SET status='running',last_error=NULL
		WHERE id=(SELECT id FROM app_backups WHERE status='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id,user_app_id::text,volume_key,docker_volume,storage_key,(SELECT user_id::text FROM user_apps WHERE id=app_backups.user_app_id)`).Scan(&id, &appID, &volumeKey, &sourceVolume, &storageKey, &userID)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("backup claim failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("backup claim commit failed", "error", err)
		return
	}

	if executor == nil {
		markBackupFailed(ctx, db, id, fmt.Errorf("docker executor is unavailable"), logger)
		return
	}
	// Do not trust a backup row copied from another Compose project. The
	// immutable app/key pair is the authoritative source for this runtime's
	// scoped volume name.
	sourceVolume = runtimepolicy.AppVolumeNameForOwner(runtimeOwner, appID, volumeKey)
	if err = executor.Pull(ctx, backupHelperImage); err != nil {
		markBackupFailed(ctx, db, id, err, logger)
		return
	}
	limitGiB, liveBytes, retainedBytes, usageErr := appBackupCapacityUsage(ctx, db, appID)
	if usageErr != nil {
		markBackupFailed(ctx, db, id, fmt.Errorf("shared data volume usage could not be measured: %w", usageErr), logger)
		return
	}
	if backupStorageQuotaExceeded(limitGiB, liveBytes, retainedBytes) {
		markBackupFailed(ctx, db, id, fmt.Errorf("shared data volume capacity is already exhausted; delete a backup or expand the application capacity"), logger)
		return
	}
	sizeBytes, err := executor.ArchiveVolume(ctx, backupHelperImage, backupVolume, sourceVolume, storageKey, id)
	if err != nil {
		markBackupFailed(ctx, db, id, err, logger)
		return
	}
	limitGiB, liveBytes, retainedBytes, usageErr = appBackupCapacityUsage(ctx, db, appID)
	if usageErr != nil || backupStorageQuotaExceededParts(limitGiB, liveBytes, retainedBytes, sizeBytes) {
		_ = executor.DeleteBackup(ctx, backupHelperImage, backupVolume, storageKey, id)
		if usageErr != nil {
			markBackupFailed(ctx, db, id, fmt.Errorf("shared data volume usage could not be verified: %w", usageErr), logger)
		} else {
			markBackupFailed(ctx, db, id, fmt.Errorf("backup exceeds the shared data volume capacity; delete an older backup or expand the application capacity"), logger)
		}
		return
	}
	tx, err = db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		markBackupFailed(ctx, db, id, err, logger)
		return
	}
	defer tx.Rollback(ctx)
	completedAt := time.Now().UTC()
	if _, err = tx.Exec(ctx, `UPDATE app_backups SET status='succeeded',size_bytes=$2,reserved_bytes=0,completed_at=$3,last_error=NULL WHERE id=$1`, id, sizeBytes, completedAt); err != nil {
		logger.Error("backup completion update failed", "backup", id, "error", err)
		_ = tx.Rollback(ctx)
		_ = executor.DeleteBackup(ctx, backupHelperImage, backupVolume, storageKey, id)
		markBackupFailed(ctx, db, id, err, logger)
		return
	}
	// The archive shares the application's single data-volume capacity and is
	// not metered as a second storage product. Only the backup operation itself
	// remains independently billable.
	windowStart := completedAt.Add(-5 * time.Minute)
	if _, err = tx.Exec(ctx, `INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key)
		SELECT $1,a.id,'backup.operation',1,'operation',$3,$4,resolve_pricing_version($1,a.id,'backup.operation','operation',$3),$5
		FROM user_apps a WHERE a.id=(SELECT user_app_id FROM app_backups WHERE id=$2)
		ON CONFLICT DO NOTHING`, userID, id, windowStart, completedAt, "backup:"+id+":operation"); err != nil {
		logger.Error("backup operation usage event failed", "backup", id, "error", err)
		_ = tx.Rollback(ctx)
		_ = executor.DeleteBackup(ctx, backupHelperImage, backupVolume, storageKey, id)
		markBackupFailed(ctx, db, id, err, logger)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("backup completion commit failed", "backup", id, "error", err)
		_ = executor.DeleteBackup(ctx, backupHelperImage, backupVolume, storageKey, id)
		markBackupFailed(ctx, db, id, err, logger)
		return
	}
	logger.Info("volume backup completed", "backup", id, "volume", sourceVolume)
}

func appBackupCapacityUsage(ctx context.Context, db *pgxpool.Pool, appID string) (string, int64, int64, error) {
	var runtimeSpec map[string]any
	if err := db.QueryRow(ctx, `SELECT release.immutable_snapshot->'runtime_spec' FROM user_apps app
		JOIN app_releases release ON release.id=app.last_successful_release_id WHERE app.id=$1`, appID).Scan(&runtimeSpec); err != nil {
		return "", 0, 0, err
	}
	capacity, err := runtimepolicy.RuntimeDataVolumeGiB(runtimeSpec, true)
	if err != nil || capacity <= 0 {
		if err == nil {
			err = fmt.Errorf("application has no shared data volume capacity")
		}
		return "", 0, 0, err
	}
	liveBytes := int64(0)
	rows, err := db.Query(ctx, `SELECT DISTINCT volume->>'name' FROM app_releases release
		CROSS JOIN LATERAL jsonb_array_elements(coalesce(release.immutable_snapshot->'runtime_spec'->'volumes','[]'::jsonb)) volume
		WHERE release.id=(SELECT last_successful_release_id FROM user_apps WHERE id=$1)
		  AND coalesce(volume->>'name','')<>''`, appID)
	if err != nil {
		return "", 0, 0, err
	}
	volumeKeys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return "", 0, 0, err
		}
		volumeKeys = append(volumeKeys, key)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return "", 0, 0, err
	}
	for _, key := range volumeKeys {
		size, sizeErr := executor.VolumeSize(ctx, runtimepolicy.AppVolumeNameForOwner(runtimeOwner, appID, key))
		if sizeErr != nil {
			return "", 0, 0, sizeErr
		}
		if size > 0 && liveBytes > math.MaxInt64-size {
			return "", 0, 0, fmt.Errorf("application volume usage overflow")
		}
		liveBytes += size
	}
	var retainedBytes int64
	if err = db.QueryRow(ctx, `SELECT coalesce(sum(backup.size_bytes),0)::bigint FROM app_backups backup
		LEFT JOIN app_backup_deletion_jobs deletion ON deletion.backup_id=backup.id
		WHERE backup.user_app_id=$1 AND backup.status='succeeded' AND coalesce(deletion.status,'') <> 'succeeded'`, appID).Scan(&retainedBytes); err != nil {
		return "", 0, 0, err
	}
	return strconv.FormatFloat(capacity, 'f', -1, 64), liveBytes, retainedBytes, nil
}

func processBackupDeletionOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, backupID, storageKey string
	var attempts int
	err = tx.QueryRow(ctx, `UPDATE app_backup_deletion_jobs deletion
		SET status='running',attempts=attempts+1,updated_at=now(),last_error=NULL
		FROM app_backups backup
		WHERE deletion.id=(SELECT id FROM app_backup_deletion_jobs
			WHERE available_at<=now() AND (status='queued' OR (status='running' AND updated_at<now()-interval '2 minutes'))
			ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		  AND backup.id=deletion.backup_id
		RETURNING deletion.id::text,deletion.backup_id::text,backup.storage_key,deletion.attempts`).Scan(&id, &backupID, &storageKey, &attempts)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("backup deletion claim failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("backup deletion claim commit failed", "error", err)
		return
	}
	if executor == nil {
		finishBackupDeletion(ctx, db, id, backupID, attempts, fmt.Errorf("docker executor is unavailable"), logger)
		return
	}
	if err = executor.Pull(ctx, backupHelperImage); err == nil {
		err = executor.DeleteBackup(ctx, backupHelperImage, backupVolume, storageKey, id)
	}
	finishBackupDeletion(ctx, db, id, backupID, attempts, err, logger)
}

func finishBackupDeletion(ctx context.Context, db *pgxpool.Pool, id, backupID string, attempts int, cause error, logger *slog.Logger) {
	if cause == nil {
		if _, err := db.Exec(ctx, `UPDATE app_backup_deletion_jobs SET status='succeeded',updated_at=now(),completed_at=now(),last_error=NULL WHERE id=$1`, id); err != nil {
			logger.Error("backup deletion completion failed", "backup", backupID, "error", err)
			return
		}
		logger.Info("backup archive deleted", "backup", backupID)
		return
	}
	if attempts >= 5 {
		_, err := db.Exec(ctx, `UPDATE app_backup_deletion_jobs SET status='failed',updated_at=now(),completed_at=now(),last_error=$2 WHERE id=$1`, id, cause.Error())
		if err != nil {
			logger.Error("backup deletion failure update failed", "backup", backupID, "error", err)
		}
		return
	}
	delay := time.Duration(attempts*attempts) * 15 * time.Second
	if _, err := db.Exec(ctx, `UPDATE app_backup_deletion_jobs SET status='queued',updated_at=now(),available_at=now()+$2::interval,last_error=$3 WHERE id=$1`, id, delay.String(), cause.Error()); err != nil {
		logger.Error("backup deletion retry update failed", "backup", backupID, "error", err)
	}
}

func backupStorageQuantity(sizeBytes int64) string {
	numerator := new(big.Int).Mul(big.NewInt(sizeBytes), big.NewInt(5))
	denominator := new(big.Int).Mul(big.NewInt(1<<30), big.NewInt(24*60))
	return new(big.Rat).SetFrac(numerator, denominator).FloatString(12)
}

func backupStorageQuotaExceeded(limitGiB string, usedBytes, additionalBytes int64) bool {
	return backupStorageQuotaExceededParts(limitGiB, usedBytes, additionalBytes)
}

func backupStorageQuotaExceededParts(limitGiB string, byteParts ...int64) bool {
	limit, ok := new(big.Rat).SetString(strings.TrimSpace(limitGiB))
	if !ok || limit.Sign() < 0 {
		return true
	}
	totalBytes := new(big.Int)
	for _, part := range byteParts {
		if part < 0 {
			return true
		}
		totalBytes.Add(totalBytes, big.NewInt(part))
	}
	totalGiB := new(big.Rat).SetFrac(totalBytes, big.NewInt(1<<30))
	return totalGiB.Cmp(limit) > 0
}

func markBackupFailed(ctx context.Context, db *pgxpool.Pool, id string, cause error, logger *slog.Logger) {
	if _, err := db.Exec(ctx, `UPDATE app_backups SET status='failed',reserved_bytes=0,last_error=$2,completed_at=now() WHERE id=$1`, id, cause.Error()); err != nil {
		logger.Error("backup failure update failed", "backup", id, "error", err)
	}
}

func processRestoreOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, appID, volumeKey, targetVolume, storageKey, container string
	err = tx.QueryRow(ctx, `UPDATE app_restore_jobs j SET status='running',last_error=NULL
		FROM app_backups b, app_routes r
		WHERE j.id=(SELECT id FROM app_restore_jobs WHERE status='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		  AND b.id=j.backup_id AND r.user_app_id=j.user_app_id
		RETURNING j.id,j.user_app_id,b.volume_key,b.docker_volume,b.storage_key,r.upstream_container`).Scan(&id, &appID, &volumeKey, &targetVolume, &storageKey, &container)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("restore claim failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("restore claim commit failed", "error", err)
		return
	}

	if executor == nil {
		// No container operation has happened, so leave the existing app state
		// running while recording the failed restore job.
		finishRestore(ctx, db, id, appID, fmt.Errorf("docker executor is unavailable"), true, logger)
		return
	}
	targetVolume = runtimepolicy.AppVolumeNameForOwner(runtimeOwner, appID, volumeKey)
	if err = executor.Pull(ctx, backupHelperImage); err != nil {
		// Pull happens before Stop; the active release is still serving traffic.
		finishRestore(ctx, db, id, appID, err, true, logger)
		return
	}
	err = executor.Stop(ctx, container)
	if err == nil {
		err = executor.RestoreVolume(ctx, backupHelperImage, backupVolume, targetVolume, storageKey, id)
	}
	startErr := executor.Start(ctx, container)
	if err == nil && startErr != nil {
		err = startErr
	}
	healthy := false
	if startErr == nil {
		healthy = waitForHealthy(ctx, container, 30*time.Second)
	}
	if err == nil && !healthy {
		err = fmt.Errorf("restored container did not become healthy")
	}
	finishRestore(ctx, db, id, appID, err, healthy, logger)
}

func waitForHealthy(ctx context.Context, container string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		healthy, err := executor.Healthy(ctx, container)
		if err == nil && healthy {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

func finishRestore(ctx context.Context, db *pgxpool.Pool, id, appID string, cause error, appHealthy bool, logger *slog.Logger) {
	status, appStatus := restoreResult(cause, appHealthy)
	var lastError any
	if cause != nil {
		lastError = cause.Error()
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		logger.Error("restore completion transaction failed", "restore", id, "error", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE app_restore_jobs SET status=$2,last_error=$3,completed_at=now() WHERE id=$1`, id, status, lastError); err == nil {
		_, err = tx.Exec(ctx, `UPDATE user_apps SET status=$2 WHERE id=$1`, appID, appStatus)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		logger.Error("restore completion update failed", "restore", id, "error", err)
		return
	}
	if cause != nil {
		logger.Error("volume restore failed", "restore", id, "error", cause)
		return
	}
	logger.Info("volume restore completed", "restore", id, "app", appID)
}

func restoreResult(cause error, appHealthy bool) (status, appStatus string) {
	if cause == nil {
		return "succeeded", "running"
	}
	if appHealthy {
		return "failed", "running"
	}
	return "failed", "failed"
}

func reconcileRouterNetworks(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	if executor == nil || routerContainer == "" {
		return
	}
	rows, err := db.Query(ctx, `SELECT DISTINCT user_id::text FROM user_apps WHERE status='running'`)
	if err != nil {
		logger.Error("router network reconciliation query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if rows.Scan(&userID) != nil {
			continue
		}
		network := runtimepolicy.UserNetworkName(runtimeOwner, userID)
		if err := executor.EnsureNetwork(ctx, network); err != nil {
			logger.Warn("router network ensure failed", "network", network, "error", err)
			continue
		}
		if err := executor.ConnectNetwork(ctx, network, routerContainer, "cloudmeter-app-router"); err != nil {
			logger.Warn("router network reconciliation failed", "network", network, "error", err)
		}
	}
}

func reconcileEgressNetworks(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	if executor == nil || egressProxyContainer == "" || egressToken == "" {
		return
	}
	rows, err := db.Query(ctx, "SELECT DISTINCT user_id::text FROM user_apps WHERE status IN ('running','updating')")
	if err != nil {
		logger.Error("egress network reconciliation query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		network := runtimepolicy.UserNetworkName(runtimeOwner, userID)
		if err := executor.EnsureNetwork(ctx, network); err != nil {
			logger.Warn("egress network ensure failed", "user", userID, "error", err)
			continue
		}
		if err := executor.ConnectNetwork(ctx, network, egressProxyContainer, "cloudmeter-egress-proxy"); err != nil {
			logger.Warn("egress proxy network reconciliation failed", "user", userID, "error", err)
		}
	}
}

func aggregateUsage(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		logger.Error("usage aggregate transaction failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `WITH claimed AS (
		SELECT id,user_id,user_app_id,usage_code,unit,window_start,window_end,price_version_id,quantity
		FROM usage_events WHERE aggregated_at IS NULL
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1000
	), grouped AS (
		SELECT user_id,user_app_id,usage_code,unit,window_start,window_end,price_version_id,sum(quantity) quantity
		FROM claimed GROUP BY user_id,user_app_id,usage_code,unit,window_start,window_end,price_version_id
	), written AS (
		INSERT INTO usage_aggregates(user_id,user_app_id,usage_code,unit,window_start,window_end,price_version_id,quantity,sealed_at,billing_disposition)
		SELECT user_id,user_app_id,usage_code,unit,window_start,window_end,price_version_id,quantity,
		       CASE WHEN price_version_id IS NULL THEN now() END,
		       CASE WHEN price_version_id IS NULL THEN 'unpriced' ELSE 'pending' END
		FROM grouped
		ON CONFLICT(user_id,user_app_id,usage_code,window_start,window_end,price_version_id)
		DO UPDATE SET quantity=usage_aggregates.quantity+EXCLUDED.quantity
		WHERE usage_aggregates.sealed_at IS NULL
		RETURNING 1
	)
	UPDATE usage_events SET aggregated_at=now() WHERE id IN (SELECT id FROM claimed)`)
	if err != nil {
		logger.Error("usage aggregate failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("usage aggregate commit failed", "error", err)
	}
}

func sealUnpricedUsage(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	result, err := db.Exec(ctx, `WITH candidates AS (
		SELECT id
		FROM usage_aggregates
		WHERE sealed_at IS NULL
		  AND billing_disposition='pending'
		  AND price_version_id IS NULL
		ORDER BY window_start,id
		FOR UPDATE SKIP LOCKED
		LIMIT 1000
	)
	UPDATE usage_aggregates aggregate
		SET sealed_at=now(),billing_disposition='unpriced'
		FROM candidates
		WHERE aggregate.id=candidates.id`)
	if err != nil {
		logger.Error("unpriced usage sealing failed", "error", err)
		return
	}
	if result.RowsAffected() > 0 {
		logger.Info("unpriced usage sealed", "windows", result.RowsAffected())
	}
}

func billUsage(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		logger.Error("usage billing transaction failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	var userID, usageCode, unit string
	var appID *string
	var aggregateID int64
	var pricingVersionID string
	var windowStart, windowEnd time.Time
	var quantity string
	err = tx.QueryRow(ctx, `SELECT a.id,a.user_id,a.user_app_id,a.usage_code,a.unit,a.window_start,a.window_end,a.quantity::text,a.price_version_id
        FROM usage_aggregates a JOIN wallets w ON w.user_id=a.user_id
        WHERE a.sealed_at IS NULL AND a.billing_disposition='pending'
          AND a.price_version_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM usage_charges c WHERE c.user_id=a.user_id AND c.user_app_id IS NOT DISTINCT FROM a.user_app_id AND c.usage_code=a.usage_code AND c.window_start=a.window_start AND c.window_end=a.window_end AND (a.user_app_id IS NULL OR c.pricing_version_id=a.price_version_id))
          AND NOT EXISTS (
            SELECT 1 FROM usage_billing_attempts ba
            WHERE ba.user_id=a.user_id AND ba.user_app_id IS NOT DISTINCT FROM a.user_app_id AND ba.usage_code=a.usage_code AND ba.window_start=a.window_start AND ba.window_end=a.window_end
              AND ba.status='insufficient_funds' AND ba.balance_cents=w.balance_cents
              AND ba.credit_balance_cents=(SELECT coalesce(sum(g.remaining_cents),0) FROM credit_grants g WHERE g.user_id=a.user_id AND g.remaining_cents>0 AND (g.expires_at IS NULL OR g.expires_at>now()))
          )
        ORDER BY a.window_start
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&aggregateID, &userID, &appID, &usageCode, &unit, &windowStart, &windowEnd, &quantity, &pricingVersionID)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("usage billing claim failed", "error", err)
		return
	}

	var amountCents int64
	err = tx.QueryRow(ctx, `SELECT v.id,
        CASE v.rounding_mode
          WHEN 'up' THEN ceil(greatest(($1::numeric-v.free_quantity),0,v.minimum_quantity)*v.unit_price_micros/1000000.0)
          WHEN 'down' THEN floor(greatest(($1::numeric-v.free_quantity),0,v.minimum_quantity)*v.unit_price_micros/1000000.0)
          ELSE round(greatest(($1::numeric-v.free_quantity),0,v.minimum_quantity)*v.unit_price_micros/1000000.0)
        END::bigint
		FROM pricing_versions v WHERE v.id=$2`, quantity, pricingVersionID).Scan(&pricingVersionID, &amountCents)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("usage price lookup failed", "error", err)
		return
	}

	var walletID string
	var balance int64
	if err = tx.QueryRow(ctx, "SELECT id,balance_cents FROM wallets WHERE user_id=$1 FOR UPDATE", userID).Scan(&walletID, &balance); err != nil {
		logger.Error("usage wallet lock failed", "error", err)
		return
	}
	type creditGrant struct {
		id        string
		remaining int64
	}
	grants := []creditGrant{}
	creditRows, creditErr := tx.Query(ctx, `SELECT id,remaining_cents FROM credit_grants WHERE user_id=$1 AND remaining_cents>0 AND (expires_at IS NULL OR expires_at>now()) ORDER BY expires_at ASC NULLS LAST,created_at,id FOR UPDATE`, userID)
	if creditErr != nil {
		logger.Error("credit grant lock failed", "error", creditErr)
		return
	}
	var creditBalance int64
	for creditRows.Next() {
		var grant creditGrant
		if creditRows.Scan(&grant.id, &grant.remaining) != nil {
			creditRows.Close()
			return
		}
		grants = append(grants, grant)
		creditBalance += grant.remaining
	}
	creditRows.Close()
	creditUsed := amountCents
	if creditUsed > creditBalance {
		creditUsed = creditBalance
	}
	walletCharge := amountCents - creditUsed
	if balance < walletCharge {
		var containers []string
		containerRows, queryErr := tx.Query(ctx, `SELECT r.upstream_container FROM app_routes r JOIN user_apps a ON a.id=r.user_app_id WHERE a.user_id=$1 AND a.status='running' AND r.upstream_container<>''`, userID)
		if queryErr != nil {
			logger.Error("billing suspension route lookup failed", "error", queryErr)
			return
		}
		for containerRows.Next() {
			var name string
			if containerRows.Scan(&name) == nil {
				containers = append(containers, name)
			}
		}
		containerRows.Close()
		_, err = tx.Exec(ctx, `INSERT INTO usage_billing_attempts(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id,amount_cents,status,balance_cents,credit_balance_cents) VALUES($1,$2,$3,$4,$5,$6,$7,'insufficient_funds',$8,$9) ON CONFLICT DO NOTHING`, userID, appID, usageCode, windowStart, windowEnd, pricingVersionID, amountCents, balance, creditBalance)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata) VALUES($1,$2,'billing_suspended','critical','余额不足，应用已暂停','赠送额度抵扣后钱包余额仍不足，请充值或联系管理员发放额度。',jsonb_build_object('usage_code',$3::text,'gross_amount_cents',$4::bigint,'credit_available_cents',$5::bigint,'required_wallet_cents',$6::bigint,'balance_cents',$7::bigint)) ON CONFLICT(user_id,event_key) DO NOTHING`, userID, fmt.Sprintf("billing-suspended/%d", aggregateID), usageCode, amountCents, creditBalance, walletCharge, balance)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE user_id=$1 AND status='running'", userID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM app_routes WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id=$1 AND status='suspended' AND suspension_reason='billing_insufficient')`, userID)
		}
		if err != nil {
			logger.Error("usage suspension failed", "error", err)
			return
		}
		if err = tx.Commit(ctx); err != nil {
			logger.Error("usage suspension commit failed", "error", err)
			return
		}
		if executor != nil {
			for _, name := range containers {
				if stopErr := executor.Stop(ctx, name); stopErr != nil {
					logger.Warn("billing suspended app stop failed", "container", name, "error", stopErr)
				}
				if removeErr := executor.Remove(ctx, name); removeErr != nil {
					logger.Warn("billing suspended app cleanup failed", "container", name, "error", removeErr)
				}
			}
		}
		return
	}

	newBalance := balance - walletCharge
	var ledgerID *int64
	if walletCharge > 0 {
		var id int64
		appRef := "account"
		if appID != nil {
			appRef = *appID
		}
		businessRef := usageCode + "/" + appRef + "/" + pricingVersionID + "/" + windowStart.Format(time.RFC3339) + "/" + windowEnd.Format(time.RFC3339)
		err = tx.QueryRow(ctx, `INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'usage',$2,$3,$4,jsonb_build_object('usage_code',$5::text,'window_start',$6::timestamptz,'window_end',$7::timestamptz,'pricing_version_id',$8::text,'gross_amount_cents',$9::bigint,'credit_amount_cents',$10::bigint)) RETURNING id`, walletID, businessRef, -walletCharge, newBalance, usageCode, windowStart, windowEnd, pricingVersionID, amountCents, creditUsed).Scan(&id)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE wallets SET balance_cents=$1,version=version+1 WHERE id=$2", newBalance, walletID)
		}
		if err != nil {
			logger.Error("usage wallet charge failed", "error", err)
			return
		}
		ledgerID = &id
	}
	var usageChargeID int64
	err = tx.QueryRow(ctx, `INSERT INTO usage_charges(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id,quantity,amount_cents,wallet_ledger_entry_id) VALUES($1,$2,$3,$4,$5,$6,$7::numeric,$8,$9) RETURNING id`, userID, appID, usageCode, windowStart, windowEnd, pricingVersionID, quantity, amountCents, ledgerID).Scan(&usageChargeID)
	remainingCredit := creditUsed
	for _, grant := range grants {
		if err != nil || remainingCredit == 0 {
			break
		}
		used := grant.remaining
		if used > remainingCredit {
			used = remainingCredit
		}
		_, err = tx.Exec(ctx, `UPDATE credit_grants SET remaining_cents=remaining_cents-$2 WHERE id=$1`, grant.id, used)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO credit_consumptions(credit_grant_id,usage_charge_id,amount_cents) VALUES($1,$2,$3)`, grant.id, usageChargeID, used)
		}
		remainingCredit -= used
	}
	var billID string
	if err == nil {
		periodStart := time.Date(windowStart.UTC().Year(), windowStart.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 1, 0)
		err = tx.QueryRow(ctx, `INSERT INTO bills(user_id,period_start,period_end) VALUES($1,$2,$3) ON CONFLICT(user_id,period_start,period_end) DO UPDATE SET updated_at=now() RETURNING id`, userID, periodStart, periodEnd).Scan(&billID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO bill_items(bill_id,usage_charge_id,user_app_id,app_slug,usage_code,unit,quantity,pricing_version_id,unit_price_micros,amount_cents,window_start,window_end) SELECT $1,$2,$3,a.slug,$4,$5,$6::numeric,$7,pv.unit_price_micros,$8,$9,$10 FROM pricing_versions pv LEFT JOIN user_apps a ON a.id=$3 WHERE pv.id=$7`, billID, usageChargeID, appID, usageCode, unit, quantity, pricingVersionID, amountCents, windowStart, windowEnd)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE bills SET total_cents=total_cents+$2,updated_at=now() WHERE id=$1`, billID, amountCents)
	}
	if err == nil {
		_, err = tx.Exec(ctx, "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='charged',price_version_id=coalesce(price_version_id,$2) WHERE id=$1", aggregateID, pricingVersionID)
	}
	if err == nil && walletCharge > 0 && newBalance > 0 && newBalance <= 100 {
		_, err = tx.Exec(ctx, `INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata) VALUES($1,$2,'low_balance','warning','账户余额较低','当前余额不足 1 元，请及时充值以避免应用暂停。',jsonb_build_object('balance_cents',$3::bigint)) ON CONFLICT(user_id,event_key) DO NOTHING`, userID, fmt.Sprintf("low-balance/%d", aggregateID), newBalance)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO usage_billing_attempts(user_id,user_app_id,usage_code,window_start,window_end,pricing_version_id,amount_cents,status,balance_cents,credit_balance_cents) VALUES($1,$2,$3,$4,$5,$6,$7,'charged',$8,$9) ON CONFLICT DO NOTHING`, userID, appID, usageCode, windowStart, windowEnd, pricingVersionID, amountCents, newBalance, creditBalance-creditUsed)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO deployment_jobs(user_app_id,release_id,idempotency_key,operation,source_release_id)
			SELECT a.id,a.last_successful_release_id,'billing-resume/' || a.id::text || '/' || gen_random_uuid()::text,'billing_recovery',a.last_successful_release_id
			FROM user_apps a WHERE a.user_id=$1 AND a.status='suspended' AND a.suspension_reason='billing_insufficient'
			  AND a.last_successful_release_id IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM usage_aggregates ua WHERE ua.user_id=$1 AND ua.sealed_at IS NULL AND ua.billing_disposition='pending')
			  AND NOT EXISTS (SELECT 1 FROM deployment_jobs j WHERE j.user_app_id=a.id AND j.state NOT IN ('succeeded','failed'))`, userID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE user_apps a SET status='updating',suspension_reason=NULL
			WHERE a.user_id=$1 AND a.status='suspended' AND a.suspension_reason='billing_insufficient'
			  AND EXISTS (SELECT 1 FROM deployment_jobs j WHERE j.user_app_id=a.id AND j.state='queued' AND j.idempotency_key LIKE 'billing-resume/%')`, userID)
	}
	if err != nil {
		logger.Error("usage charge recording failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("usage billing commit failed", "error", err)
		return
	}
	logger.Info("usage charged", "user", userID, "usage_code", usageCode, "amount_cents", amountCents)
}

func meterRuntime(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	now := time.Now().UTC()
	windowEnd := now.Truncate(5 * time.Minute)
	windowStart := windowEnd.Add(-5 * time.Minute)
	if windowEnd.After(now) || windowEnd.Equal(windowStart) {
		return
	}
	rows, err := db.Query(ctx, `SELECT a.id,a.user_id,coalesce(ar.upstream_container,''),
		coalesce(r.immutable_snapshot->'runtime_spec'->>'cpuCores',''),
		coalesce(r.immutable_snapshot->'runtime_spec'->>'memoryMiB','')
		FROM user_apps a
		JOIN app_releases r ON r.id=a.last_successful_release_id
		LEFT JOIN app_routes ar ON ar.user_app_id=a.id
		WHERE a.status='running'`)
	if err != nil {
		logger.Error("usage query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var appID, userID, container, cpuCores, memoryMiB string
		if err := rows.Scan(&appID, &userID, &container, &cpuCores, &memoryMiB); err != nil {
			logger.Error("usage scan failed", "error", err)
			continue
		}
		if executor == nil || container == "" {
			continue
		}
		cpuUsage, memoryUsage, statsErr := executor.Stats(ctx, container)
		if statsErr != nil {
			logger.Warn("runtime usage interval not confirmed", "app", appID, "container", container, "error", statsErr)
			continue
		}
		if _, err := db.Exec(ctx, `INSERT INTO app_runtime_metrics(user_app_id,cpu_usage_cores,memory_usage_bytes,sampled_at)
			VALUES($1,$2,$3,now())
			ON CONFLICT(user_app_id) DO UPDATE SET cpu_usage_cores=EXCLUDED.cpu_usage_cores,
			memory_usage_bytes=EXCLUDED.memory_usage_bytes,sampled_at=EXCLUDED.sampled_at`, appID, cpuUsage, memoryUsage); err != nil {
			logger.Warn("runtime metrics update failed", "app", appID, "error", err)
		}
		key := appID + ":runtime:" + windowStart.Format(time.RFC3339)
		if _, err := db.Exec(ctx, "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES($1,$2,'app.runtime.minutes',5,'minute',$3,$4,resolve_pricing_version($1,$2,'app.runtime.minutes','minute',$3),$5) ON CONFLICT DO NOTHING", userID, appID, windowStart, windowEnd, key); err != nil {
			logger.Error("usage insert failed", "app", appID, "error", err)
		}
		insertUsage(ctx, db, userID, appID, "cpu.core_hours", "core_hour", decimalTimeQuantity(cpuCores, 5, 60), windowStart, windowEnd, appID+":cpu:"+windowStart.Format(time.RFC3339), logger)
		insertUsage(ctx, db, userID, appID, "memory.gib_hours", "GiB_hour", decimalTimeQuantity(memoryMiB, 5, 1024*60), windowStart, windowEnd, appID+":memory:"+windowStart.Format(time.RFC3339), logger)
	}
}

// Persistent volumes remain billable while an application is stopped. Query
// every immutable release so volumes created by a failed deployment are also
// measured when they still exist in Docker.
func meterStorage(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	if executor == nil {
		return
	}
	now := time.Now().UTC()
	windowEnd := now.Truncate(5 * time.Minute)
	windowStart := windowEnd.Add(-5 * time.Minute)
	if windowEnd.After(now) || windowEnd.Equal(windowStart) {
		return
	}
	rows, err := db.Query(ctx, `SELECT app.id::text,app.user_id::text,volume->>'name',
		max(coalesce((release.immutable_snapshot->'runtime_spec'->>'dataVolumeGiB')::numeric,(volume->>'sizeGiB')::numeric,10)) OVER (PARTITION BY app.id)::text
		FROM user_apps app
		JOIN app_releases release ON release.id=app.last_successful_release_id
		CROSS JOIN LATERAL jsonb_array_elements(coalesce(release.immutable_snapshot->'runtime_spec'->'volumes','[]'::jsonb)) volume
		WHERE coalesce(volume->>'name','')<>'' AND app.deleted_at IS NULL`)
	if err != nil {
		logger.Error("storage usage query failed", "error", err)
		return
	}
	type storageApp struct {
		userID, capacityGiB string
		volumeKeys          map[string]bool
	}
	apps := map[string]*storageApp{}
	for rows.Next() {
		var appID, userID, key, sizeGiB string
		if err = rows.Scan(&appID, &userID, &key, &sizeGiB); err != nil {
			logger.Error("storage usage scan failed", "error", err)
			continue
		}
		item := apps[appID]
		if item == nil {
			item = &storageApp{userID: userID, capacityGiB: sizeGiB, volumeKeys: map[string]bool{}}
			apps[appID] = item
		}
		item.volumeKeys[key] = true
	}
	if err = rows.Err(); err != nil {
		logger.Error("storage usage iteration failed", "error", err)
		rows.Close()
		return
	}
	rows.Close()
	for appID, item := range apps {
		observed := false
		for key := range item.volumeKeys {
			size, sizeErr := executor.VolumeSize(ctx, runtimepolicy.AppVolumeNameForOwner(runtimeOwner, appID, key))
			if sizeErr != nil {
				continue
			}
			observed = true
			if _, metricErr := db.Exec(ctx, `INSERT INTO app_storage_metrics(user_app_id,volume_key,usage_bytes,sampled_at) VALUES($1,$2,$3,now())
				ON CONFLICT(user_app_id,volume_key) DO UPDATE SET usage_bytes=EXCLUDED.usage_bytes,sampled_at=EXCLUDED.sampled_at`, appID, key, size); metricErr != nil {
				logger.Warn("storage metric update failed", "app", appID, "volume", key, "error", metricErr)
			}
		}
		if observed {
			insertUsage(ctx, db, item.userID, appID, "storage.data.gib_days", "GiB_day", decimalTimeQuantity(item.capacityGiB, 5, 24*60), windowStart, windowEnd, appID+":data-storage:shared:"+windowStart.Format(time.RFC3339), logger)
		}
	}
}

func meterEgress(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	closedBefore := time.Now().UTC().Truncate(5 * time.Minute)
	rows, err := db.Query(ctx, `SELECT s.user_app_id,a.user_id,
		to_timestamp(floor(extract(epoch from s.observed_at)/300)*300) AS window_start
		FROM app_egress_samples s JOIN user_apps a ON a.id=s.user_app_id
		WHERE s.processed_at IS NULL AND s.observed_at < $1
		GROUP BY s.user_app_id,a.user_id,window_start ORDER BY window_start LIMIT 100`, closedBefore)
	if err != nil {
		logger.Error("egress meter query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var appID, userID string
		var windowStart time.Time
		if err := rows.Scan(&appID, &userID, &windowStart); err != nil {
			logger.Error("egress meter scan failed", "error", err)
			continue
		}
		windowEnd := windowStart.Add(5 * time.Minute)
		key := appID + ":egress:" + windowStart.Format(time.RFC3339)
		tx, txErr := db.Begin(ctx)
		var delta, cumulative int64
		if txErr == nil {
			_, txErr = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", appID)
		}
		if txErr == nil {
			txErr = tx.QueryRow(ctx, `SELECT coalesce(sum(byte_delta),0),coalesce(max(cumulative_bytes),0) FROM app_egress_samples
				WHERE user_app_id=$1 AND processed_at IS NULL AND observed_at >= $2 AND observed_at < $3`, appID, windowStart, windowEnd).Scan(&delta, &cumulative)
		}
		if txErr == nil && delta > 0 {
			_, txErr = tx.Exec(ctx, `INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key)
				VALUES($1,$2,'network.egress_gib',$3::numeric / 1073741824,'GiB',$4,$5,resolve_pricing_version($1,$2,'network.egress_gib','GiB',$4),$6) ON CONFLICT DO NOTHING`, userID, appID, delta, windowStart, windowEnd, key)
		}
		if txErr == nil {
			_, txErr = tx.Exec(ctx, `UPDATE app_egress_samples SET processed_at=now() WHERE user_app_id=$1 AND processed_at IS NULL AND observed_at >= $2 AND observed_at < $3`, appID, windowStart, windowEnd)
		}
		if txErr == nil {
			_, txErr = tx.Exec(ctx, `INSERT INTO app_egress_billing_cursors(user_app_id,billed_bytes) VALUES($1,$2)
                ON CONFLICT(user_app_id) DO UPDATE SET billed_bytes=GREATEST(app_egress_billing_cursors.billed_bytes,EXCLUDED.billed_bytes),updated_at=now()`, appID, cumulative)
		}
		if txErr == nil {
			txErr = tx.Commit(ctx)
		} else if tx != nil {
			_ = tx.Rollback(ctx)
		}
		if txErr != nil {
			logger.Error("egress meter commit failed", "app", appID, "error", txErr)
			continue
		}
	}
}

func insertUsage(ctx context.Context, db *pgxpool.Pool, userID, appID, code, unit, quantity string, start, end time.Time, key string, logger *slog.Logger) {
	if quantity == "" || strings.HasPrefix(quantity, "-") {
		return
	}
	if _, err := db.Exec(ctx, "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES($1,$2,$3,$4::numeric,$5,$6,$7,resolve_pricing_version($1,$2,$3,$5,$6),$8) ON CONFLICT DO NOTHING", userID, appID, code, quantity, unit, start, end, key); err != nil {
		logger.Error("resource usage insert failed", "app", appID, "usage_code", code, "error", err)
	}
}

func formatQuantity(value float64) string {
	if value <= 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', 12, 64)
}

func bytesQuantity(bytes int64, numerator, minutes int64) string {
	if bytes <= 0 {
		return "0"
	}
	n := new(big.Int).Mul(big.NewInt(bytes), big.NewInt(numerator))
	d := new(big.Int).Mul(big.NewInt(1<<30), big.NewInt(minutes))
	return new(big.Rat).SetFrac(n, d).FloatString(12)
}

func decimalTimeQuantity(value string, numerator, denominator int64) string {
	quantity, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || quantity.Sign() <= 0 || numerator <= 0 || denominator <= 0 {
		return "0"
	}
	quantity.Mul(quantity, new(big.Rat).SetFrac(big.NewInt(numerator), big.NewInt(denominator)))
	return quantity.FloatString(12)
}

func egressQuotaExceeded(limit, used string, deltaBytes int64) bool {
	limitRat, okLimit := new(big.Rat).SetString(strings.TrimSpace(limit))
	usedRat, okUsed := new(big.Rat).SetString(strings.TrimSpace(used))
	if !okLimit || !okUsed || deltaBytes < 0 {
		return true
	}
	deltaRat := new(big.Rat).SetFrac(big.NewInt(deltaBytes), big.NewInt(1<<30))
	usedRat.Add(usedRat, deltaRat)
	return usedRat.Cmp(limitRat) > 0
}

func declaredSecretVersions(runtimeSpec map[string]any, versions map[string]any) (map[string]string, error) {
	keys, err := runtimepolicy.RuntimeSecretKeys(runtimeSpec)
	if err != nil {
		return nil, err
	}
	declared := make(map[string]string, len(keys))
	for _, key := range keys {
		rawVersion, exists := versions[key]
		versionID, ok := rawVersion.(string)
		if !exists || !ok || !regexpUUID.MatchString(versionID) {
			return nil, fmt.Errorf("declared Secret %s has no valid version reference", key)
		}
		declared[key] = versionID
	}
	return declared, nil
}

func processOne(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	var oldContainer string
	var rollbackContainer string
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, appID, releaseID, operation string
	var attempts int
	var state domain.DeploymentState
	var snapshot []byte
	err = tx.QueryRow(ctx, `SELECT j.id,j.user_app_id,j.release_id,j.state,j.operation,j.attempts,r.immutable_snapshot
		FROM deployment_jobs j JOIN app_releases r ON r.id=j.release_id AND r.user_app_id=j.user_app_id
		WHERE j.state NOT IN ('succeeded','failed') AND j.available_at<=now()
		ORDER BY j.created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &appID, &releaseID, &state, &operation, &attempts, &snapshot)
	if err == pgx.ErrNoRows {
		return
	}
	if err != nil {
		logger.Error("claim failed", "error", err)
		return
	}
	healthy := true
	if state == domain.DeploymentPulling && executor != nil {
		var userID string
		if err = tx.QueryRow(ctx, `SELECT user_id::text FROM user_apps WHERE id=$1`, appID).Scan(&userID); err != nil {
			logger.Error("user lookup failed", "error", err)
			return
		}
		var snap struct {
			Image   string         `json:"image_digest"`
			Runtime map[string]any `json:"runtime_spec"`
		}
		if json.Unmarshal(snapshot, &snap) != nil || snap.Image == "" {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, fmt.Errorf("release image is missing"), logger)
			return
		}
		missing, dependencyErr := unavailableRuntimeDependencies(ctx, tx, userID, appID, snap.Runtime)
		if dependencyErr != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, fmt.Errorf("dependency verification failed: %w", dependencyErr), logger)
			return
		}
		if len(missing) > 0 {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, fmt.Errorf("required dependencies are unavailable: %s", strings.Join(missing, ", ")), logger)
			return
		}
		network := runtimepolicy.UserNetworkName(runtimeOwner, userID)
		if err = executor.EnsureNetwork(ctx, network); err != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, err, logger)
			return
		}
		if err = executor.ConnectNetwork(ctx, network, routerContainer, "cloudmeter-app-router"); err != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, err, logger)
			return
		}
		if egressProxyContainer != "" {
			if err = executor.ConnectNetwork(ctx, network, egressProxyContainer, "cloudmeter-egress-proxy"); err != nil {
				tx.Rollback(ctx)
				markJobError(ctx, db, id, err, logger)
				return
			}
		}
		if _, err = pullConfiguredProductImage(ctx, db, snap.Image); err != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, err, logger)
			return
		}
		var healthSnapshot struct {
			Health map[string]any `json:"health_spec"`
		}
		if json.Unmarshal(snapshot, &healthSnapshot) != nil {
			// The full snapshot is validated again when the job reaches the
			// health-check state; do not make image preparation fail early.
			healthSnapshot.Health = nil
		}
		if _, needsProbe := healthPath(healthSnapshot.Health); needsProbe {
			if err = executor.Pull(ctx, backupHelperImage); err != nil {
				tx.Rollback(ctx)
				markJobError(ctx, db, id, fmt.Errorf("health probe helper image pull failed: %w", err), logger)
				return
			}
		}
	}
	if state == domain.DeploymentStarting && executor != nil {
		var userID, serviceSlug, image string
		if err = tx.QueryRow(ctx, `SELECT user_id::text,service_slug FROM user_apps WHERE id=$1`, appID).Scan(&userID, &serviceSlug); err != nil {
			logger.Error("user lookup failed", "error", err)
			return
		}
		var snap struct {
			Image          string         `json:"image_digest"`
			Runtime        map[string]any `json:"runtime_spec"`
			SecretVersions map[string]any `json:"secret_versions"`
		}
		if decodeErr := json.Unmarshal(snapshot, &snap); decodeErr != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, fmt.Errorf("release snapshot is invalid: %w", decodeErr), logger)
			return
		}
		image = snap.Image
		image, err = configuredProductImage(ctx, db, image)
		if err != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, err, logger)
			return
		}
		if snap.Runtime == nil {
			snap.Runtime = map[string]any{}
		}
		declaredVersions, secretConfigErr := declaredSecretVersions(snap.Runtime, snap.SecretVersions)
		if secretConfigErr != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, fmt.Errorf("release Secret configuration is invalid: %w", secretConfigErr), logger)
			return
		}
		secretEnv := map[string]any{}
		if existing, ok := snap.Runtime["env"].(map[string]any); ok {
			for key, value := range existing {
				secretEnv[key] = value
			}
		}
		for key, versionID := range declaredVersions {
			var encrypted string
			queryErr := tx.QueryRow(ctx, `SELECT v.encrypted_value FROM app_secret_versions v JOIN app_secrets s ON s.id=v.app_secret_id WHERE v.id=$1 AND s.user_app_id=$2 AND s.key=$3`, versionID, appID, key).Scan(&encrypted)
			if queryErr != nil {
				tx.Rollback(ctx)
				markJobError(ctx, db, id, fmt.Errorf("release secret reference is invalid"), logger)
				return
			}
			plaintext, decryptErr := secrets.Decrypt("app.secret.version."+versionID, encrypted)
			if decryptErr != nil {
				tx.Rollback(ctx)
				markJobError(ctx, db, id, fmt.Errorf("release secret authentication failed"), logger)
				return
			}
			secretEnv[key] = plaintext
		}
		if len(secretEnv) > 0 {
			snap.Runtime["env"] = secretEnv
		}
		if egressToken != "" {
			mac := hmac.New(sha256.New, []byte(egressToken))
			_, _ = mac.Write([]byte(appID))
			password := hex.EncodeToString(mac.Sum(nil))
			proxyURL := "http://" + appID + ":" + password + "@cloudmeter-egress-proxy:3128"
			secretEnv["HTTP_PROXY"] = proxyURL
			secretEnv["HTTPS_PROXY"] = proxyURL
			secretEnv["http_proxy"] = proxyURL
			secretEnv["https_proxy"] = proxyURL
			noProxy := dependencyNoProxy(snap.Runtime)
			secretEnv["NO_PROXY"] = strings.Join(noProxy, ",")
			secretEnv["no_proxy"] = strings.Join(noProxy, ",")
			snap.Runtime["env"] = secretEnv
		}
		snap.Runtime["appId"] = appID
		name := containerName(appID, releaseID)
		if err = executor.Create(ctx, name, image, runtimepolicy.UserNetworkName(runtimeOwner, userID), []string{serviceSlug, releaseAlias(releaseID)}, snap.Runtime); err != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, err, logger)
			return
		}
		if err = executor.Start(ctx, name); err != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, err, logger)
			return
		}
	}
	healthFailure := ""
	if state == domain.DeploymentChecking && executor != nil {
		var healthErr error
		healthy, healthErr = executor.Healthy(ctx, containerName(appID, releaseID))
		if healthErr != nil {
			tx.Rollback(ctx)
			markJobError(ctx, db, id, healthErr, logger)
			return
		}
		if !healthy {
			healthFailure = "application container is not ready"
		}
		if healthy {
			var probe struct {
				Route  map[string]any `json:"route_spec"`
				Health map[string]any `json:"health_spec"`
			}
			if json.Unmarshal(snapshot, &probe) != nil {
				healthy = false
				healthFailure = "release health specification is invalid"
			} else if path, ok := healthPath(probe.Health); ok {
				var userID string
				if err = tx.QueryRow(ctx, `SELECT user_id::text FROM user_apps WHERE id=$1`, appID).Scan(&userID); err != nil {
					logger.Error("health probe user lookup failed", "error", err)
					return
				}
				target := fmt.Sprintf("http://%s:%d%s", releaseAlias(releaseID), routePort(probe.Route), path)
				probeName := healthProbeName(id)
				probeCtx, cancel := context.WithTimeout(ctx, time.Duration(healthTimeout(probe.Health)+5)*time.Second)
				probeErr := executor.ProbeHTTP(probeCtx, probeName, backupHelperImage, runtimepolicy.UserNetworkName(runtimeOwner, userID), target, healthTimeout(probe.Health), healthAcceptedStatusCodes(probe.Health))
				cancel()
				healthy = probeErr == nil
				if probeErr != nil {
					healthFailure = probeErr.Error()
					logger.Warn("release health probe failed", "job", id, "target", target, "error", probeErr)
				}
			}
		}
		if healthy && !snapshotHealthOK(snapshot) {
			healthy = false
			healthFailure = "release health specification rejected the deployment"
		}
	}
	healthAttempt := deploymentHealthAttempt(attempts)
	next, ok := nextDeploymentStateWithHealth(state, healthy, healthAttempt)
	if !ok || domain.ValidateDeploymentTransition(state, next) != nil {
		logger.Error("invalid job state", "job", id, "error", err)
		return
	}
	if state == domain.DeploymentChecking && !healthy && next == domain.DeploymentRollingBack {
		healthFailure = fmt.Sprintf("health check did not pass after %d attempts: %s", healthAttempt, healthFailure)
	}
	if _, err = tx.Exec(ctx, `UPDATE deployment_jobs
		SET state=$2,attempts=attempts+1,
		    available_at=CASE WHEN $2='health_checking' THEN now()+make_interval(secs=>$3) ELSE now() END,
		    last_error=CASE WHEN $4<>'' THEN $4 WHEN $2 IN ('switching_route','succeeded') THEN NULL ELSE last_error END,
		    updated_at=now()
		WHERE id=$1`, id, next, healthInterval(snapshot), healthFailure); err != nil {
		logger.Error("job update failed", "error", err)
		return
	}
	message := "deployment progressed"
	if next == domain.DeploymentRollingBack {
		if !isRecoveryOperation(operation) {
			_, err = tx.Exec(ctx, `UPDATE app_releases SET state='failed' WHERE id=$1 AND user_app_id=$2`, releaseID, appID)
		}
		if err != nil {
			logger.Error("release rollback mark failed", "error", err)
			return
		}
		message = "health check failed; route remains on previous release"
	}
	if next == domain.DeploymentSucceeded {
		if _, err = tx.Exec(ctx, `UPDATE user_apps SET status='running',suspension_reason=NULL,last_successful_release_id=$2 WHERE id=$1`, appID, releaseID); err != nil {
			logger.Error("app update failed", "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO user_notifications(user_id,event_key,kind,severity,title,content,metadata)
			SELECT a.user_id,'billing-recovered/'||j.id::text,'billing_recovered','info','应用已自动恢复','充值入账并完成欠费用量扣费后，应用已重新部署。',jsonb_build_object('app_id',a.id::text,'job_id',j.id::text)
			FROM user_apps a JOIN deployment_jobs j ON j.user_app_id=a.id WHERE a.id=$1 AND j.id=$2 AND j.operation='billing_recovery' ON CONFLICT(user_id,event_key) DO NOTHING`, appID, id); err != nil {
			logger.Error("billing recovery notification failed", "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE app_releases SET state='active' WHERE id=$1 AND user_app_id=$2`, releaseID, appID); err != nil {
			logger.Error("release update failed", "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE app_releases SET state='superseded' WHERE user_app_id=$1 AND id<>$2 AND state='active'`, appID, releaseID); err != nil {
			logger.Error("old release supersede failed", "error", err)
			return
		}
		// Deployment and product authorization are independent usage items.
		// Missing pricing versions resolve to NULL and are ignored by billing.
		if _, err = tx.Exec(ctx, `INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key)
			SELECT a.user_id,a.id,'app.deployment',1,'deployment',j.created_at,greatest(now(),j.created_at+interval '1 microsecond'),resolve_pricing_version(a.user_id,a.id,'app.deployment','deployment',j.created_at),'deployment:'||j.id::text||':deployment'
			FROM user_apps a JOIN deployment_jobs j ON j.user_app_id=a.id
			WHERE a.id=$1 AND j.id=$2 AND j.operation IN ('deploy','update','rollback') ON CONFLICT DO NOTHING`, appID, id); err != nil {
			logger.Error("deployment usage insert failed", "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key)
			SELECT a.user_id,a.id,'product.authorization',1,'authorization',a.created_at,greatest(now(),a.created_at+interval '1 microsecond'),resolve_pricing_version(a.user_id,a.id,'product.authorization','authorization',a.created_at),'app:'||a.id::text||':authorization'
			FROM user_apps a JOIN deployment_jobs j ON j.user_app_id=a.id WHERE a.id=$1 AND j.id=$2 ON CONFLICT DO NOTHING`, appID, id); err != nil {
			logger.Error("product authorization usage insert failed", "error", err)
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key)
			SELECT a.user_id,a.id,'network.public_ingress',1,'ingress',a.created_at,greatest(now(),a.created_at+interval '1 microsecond'),resolve_pricing_version(a.user_id,a.id,'network.public_ingress','ingress',a.created_at),'app:'||a.id::text||':public-ingress'
			FROM user_apps a JOIN deployment_jobs j ON j.user_app_id=a.id WHERE a.id=$1 AND j.id=$2 ON CONFLICT DO NOTHING`, appID, id); err != nil {
			logger.Error("public ingress usage insert failed", "error", err)
			return
		}
		var slug, serviceSlug, userSlug string
		if err = tx.QueryRow(ctx, `SELECT u.slug,a.slug,a.service_slug FROM user_apps a JOIN users u ON u.id=a.user_id WHERE a.id=$1`, appID).Scan(&userSlug, &slug, &serviceSlug); err != nil {
			logger.Error("route slug lookup failed", "error", err)
			return
		}
		var routeSnapshot struct {
			Route map[string]any `json:"route_spec"`
		}
		_ = json.Unmarshal(snapshot, &routeSnapshot)
		port := routePort(routeSnapshot.Route)
		_ = tx.QueryRow(ctx, `SELECT upstream_container FROM app_routes WHERE user_app_id=$1`, appID).Scan(&oldContainer)
		if _, err = tx.Exec(ctx, `INSERT INTO app_routes(user_app_id,release_id,public_path,upstream_host,upstream_port,upstream_container) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(user_app_id) DO UPDATE SET release_id=EXCLUDED.release_id,public_path=EXCLUDED.public_path,upstream_host=EXCLUDED.upstream_host,upstream_port=EXCLUDED.upstream_port,upstream_container=EXCLUDED.upstream_container,updated_at=now()`, appID, releaseID, `/apps/`+userSlug+`/`+slug, releaseAlias(releaseID), port, containerName(appID, releaseID)); err != nil {
			logger.Error("route update failed", "error", err)
			return
		}
		message = "deployment completed"
	}
	if next == domain.DeploymentFailed {
		if state == domain.DeploymentRollingBack {
			rollbackContainer = containerName(appID, releaseID)
			var hasLast bool
			if err = tx.QueryRow(ctx, `SELECT last_successful_release_id IS NOT NULL FROM user_apps WHERE id=$1`, appID).Scan(&hasLast); err != nil {
				logger.Error("app rollback state lookup failed", "error", err)
				return
			}
			status, reason := deploymentFailureState(operation, hasLast)
			if _, err = tx.Exec(ctx, `UPDATE user_apps SET status=$2,suspension_reason=nullif($3,'') WHERE id=$1`, appID, status, reason); err != nil {
				logger.Error("app rollback restore failed", "error", err)
				return
			}
			message = "rollback completed; previous release retained"
		} else {
			var hasLast bool
			if err = tx.QueryRow(ctx, `SELECT last_successful_release_id IS NOT NULL FROM user_apps WHERE id=$1`, appID).Scan(&hasLast); err != nil {
				logger.Error("app failure state lookup failed", "error", err)
				return
			}
			status, reason := deploymentFailureState(operation, hasLast)
			if _, err = tx.Exec(ctx, `UPDATE user_apps SET status=$2,suspension_reason=nullif($3,'') WHERE id=$1`, appID, status, reason); err != nil {
				logger.Error("app failure update failed", "error", err)
				return
			}
		}
		if state != domain.DeploymentRollingBack && !isRecoveryOperation(operation) {
			if _, err = tx.Exec(ctx, `UPDATE app_releases SET state='failed' WHERE id=$1 AND user_app_id=$2`, releaseID, appID); err != nil {
				logger.Error("release failure update failed", "error", err)
				return
			}
		}
		if state != domain.DeploymentRollingBack {
			message = "deployment failed"
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO deployment_events(deployment_job_id,from_state,to_state,message) VALUES($1,$2,$3,$4)`, id, state, next, message); err != nil {
		logger.Error("event insert failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("commit failed", "error", err)
		return
	}
	logger.Info("deployment claimed", "job", id, "state", next)
	if oldContainer != "" && oldContainer != containerName(appID, releaseID) && executor != nil {
		if removeErr := executor.Remove(ctx, oldContainer); removeErr != nil {
			logger.Warn("old release cleanup failed", "container", oldContainer, "error", removeErr)
		} else {
			logger.Info("old release container removed", "container", oldContainer)
		}
	}
	if rollbackContainer != "" && executor != nil {
		if removeErr := executor.Remove(ctx, rollbackContainer); removeErr != nil {
			logger.Warn("failed release container cleanup failed", "container", rollbackContainer, "error", removeErr)
		}
	}
}

func nextDeploymentState(state domain.DeploymentState) (domain.DeploymentState, bool) {
	switch state {
	case domain.DeploymentQueued:
		return domain.DeploymentPulling, true
	case domain.DeploymentPulling:
		return domain.DeploymentStarting, true
	case domain.DeploymentStarting:
		return domain.DeploymentChecking, true
	case domain.DeploymentChecking:
		return domain.DeploymentSwitching, true
	case domain.DeploymentSwitching:
		return domain.DeploymentSucceeded, true
	default:
		return "", false
	}
}

func isRecoveryOperation(operation string) bool {
	return operation == "billing_recovery" || operation == "subscription_recovery"
}

func deploymentFailureState(operation string, hasLast bool) (string, string) {
	switch operation {
	case "start":
		return "stopped", ""
	case "billing_recovery":
		return "suspended", "billing_insufficient"
	case "subscription_recovery":
		return "suspended", "subscription_expired"
	default:
		if hasLast {
			return "running", ""
		}
		return "failed", ""
	}
}

func nextDeploymentStateWithHealth(state domain.DeploymentState, healthy bool, healthAttempt int) (domain.DeploymentState, bool) {
	if state == domain.DeploymentChecking && !healthy {
		if healthAttempt < deploymentMaxHealthAttempts {
			return domain.DeploymentChecking, true
		}
		return domain.DeploymentRollingBack, true
	}
	// Rolling back is a terminal recovery step: the route was never switched,
	// so the next pass only records the failed deployment and restores app state.
	if state == domain.DeploymentRollingBack {
		return domain.DeploymentFailed, true
	}
	return nextDeploymentState(state)
}

func deploymentHealthAttempt(attempts int) int {
	// queued, pulling and starting each consume one attempt before the first
	// health-check pass. Keep older in-flight jobs bounded if their count is
	// unexpectedly lower.
	attempt := attempts - 2
	if attempt < 1 {
		return 1
	}
	return attempt
}

func snapshotHealthOK(snapshot []byte) bool {
	var value struct {
		HealthSpec map[string]any `json:"health_spec"`
	}
	if len(snapshot) == 0 || json.Unmarshal(snapshot, &value) != nil {
		return true
	}
	if failed, ok := value.HealthSpec["simulateFailure"].(bool); ok && failed {
		return false
	}
	return true
}

func healthPath(spec map[string]any) (string, bool) {
	path, ok := spec["path"].(string)
	path = strings.TrimSpace(path)
	if !ok || path == "" {
		return "", false
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \r\n\t") {
		return "/__invalid_health_path__", true
	}
	return path, true
}

func healthTimeout(spec map[string]any) int {
	value, ok := spec["timeoutSeconds"].(float64)
	if !ok || value < 1 || value > 30 {
		return 5
	}
	return int(value)
}

func healthAcceptedStatusCodes(spec map[string]any) []int {
	values, ok := spec["acceptedStatusCodes"].([]any)
	if !ok {
		return nil
	}
	result := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, raw := range values {
		value, number := raw.(float64)
		statusCode := int(value)
		if !number || value != float64(statusCode) || statusCode < 100 || statusCode > 599 || seen[statusCode] {
			continue
		}
		seen[statusCode] = true
		result = append(result, statusCode)
	}
	return result
}

func healthInterval(snapshot []byte) int {
	var value struct {
		HealthSpec map[string]any `json:"health_spec"`
	}
	if len(snapshot) == 0 || json.Unmarshal(snapshot, &value) != nil {
		return 5
	}
	interval, ok := value.HealthSpec["intervalSeconds"].(float64)
	if !ok || interval < 1 || interval > 120 {
		return 5
	}
	return int(interval)
}

func runtimeScopeToken(owner string) string {
	return runtimepolicy.ResourceScopeToken(owner)
}

func containerName(appID, releaseID string) string {
	if runtimeScope == "" {
		return "cm-" + appID + "-" + releaseID
	}
	return "cm-" + runtimeScope + "-" + appID + "-" + releaseID
}
func healthProbeName(jobID string) string {
	compact := strings.ReplaceAll(jobID, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	if runtimeScope == "" {
		return "cm-health-" + compact
	}
	return "cm-health-" + runtimeScope + "-" + compact
}
func releaseAlias(releaseID string) string {
	compact := strings.ReplaceAll(releaseID, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	return "release-" + compact
}
func routePort(spec map[string]any) int {
	if value, ok := spec["port"].(float64); ok && value >= 1 && value <= 65535 {
		return int(value)
	}
	if value, ok := spec["containerPort"].(float64); ok && value >= 1 && value <= 65535 {
		return int(value)
	}
	return 8080
}

func unavailableRuntimeDependencies(ctx context.Context, tx pgx.Tx, userID, appID string, runtimeSpec map[string]any) ([]string, error) {
	dependencies, err := runtimepolicy.RuntimeDependencies(runtimeSpec)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0)
	for _, dependency := range dependencies {
		if !dependency.Required {
			continue
		}
		var container string
		err = tx.QueryRow(ctx, `SELECT route.upstream_container FROM user_apps app JOIN app_routes route ON route.user_app_id=app.id
			WHERE app.user_id=$1 AND app.product_id=$2 AND app.service_slug=$3 AND app.id<>$4
			  AND app.status='running' AND app.last_successful_release_id IS NOT NULL
			  AND route.release_id=app.last_successful_release_id LIMIT 1`, userID, dependency.ProductID, dependency.ServiceSlug, appID).Scan(&container)
		if err == pgx.ErrNoRows {
			missing = append(missing, dependency.Key+" ("+dependency.ServiceSlug+")")
			continue
		}
		if err != nil {
			return nil, err
		}
		healthy, healthErr := executor.Healthy(ctx, container)
		if healthErr != nil || !healthy {
			missing = append(missing, dependency.Key+" ("+dependency.ServiceSlug+")")
		}
	}
	return missing, nil
}

func dependencyNoProxy(runtimeSpec map[string]any) []string {
	values := []string{"localhost", "127.0.0.1", "::1"}
	dependencies, err := runtimepolicy.RuntimeDependencies(runtimeSpec)
	if err != nil {
		return values
	}
	for _, dependency := range dependencies {
		values = append(values, dependency.ServiceSlug)
	}
	return values
}

func markJobError(ctx context.Context, db *pgxpool.Pool, id string, cause error, logger *slog.Logger) {
	tx, err := db.Begin(ctx)
	if err != nil {
		logger.Error("job error transaction failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	var appID, releaseID, operation string
	var state domain.DeploymentState
	if err = tx.QueryRow(ctx, `SELECT user_app_id,release_id,state,operation FROM deployment_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&appID, &releaseID, &state, &operation); err != nil {
		logger.Error("job error lookup failed", "error", err)
		return
	}
	if state == domain.DeploymentFailed || state == domain.DeploymentSucceeded {
		return
	}
	if domain.ValidateDeploymentTransition(state, domain.DeploymentFailed) != nil {
		logger.Error("job error transition rejected", "job", id, "state", state)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE deployment_jobs SET state='failed',last_error=$2,attempts=attempts+1,updated_at=now() WHERE id=$1`, id, cause.Error()); err != nil {
		logger.Error("job error update failed", "error", err)
		return
	}
	if !isRecoveryOperation(operation) {
		if _, err = tx.Exec(ctx, `UPDATE app_releases SET state='failed' WHERE id=$1 AND user_app_id=$2`, releaseID, appID); err != nil {
			logger.Error("job error release update failed", "error", err)
			return
		}
	}
	var hasLast bool
	if err = tx.QueryRow(ctx, `SELECT last_successful_release_id IS NOT NULL FROM user_apps WHERE id=$1`, appID).Scan(&hasLast); err != nil {
		logger.Error("job error app state lookup failed", "error", err)
		return
	}
	status, reason := deploymentFailureState(operation, hasLast)
	if _, err = tx.Exec(ctx, `UPDATE user_apps SET status=$2,suspension_reason=nullif($3,'') WHERE id=$1`, appID, status, reason); err != nil {
		logger.Error("job error app update failed", "error", err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO deployment_events(deployment_job_id,from_state,to_state,message) VALUES($1,$2,'failed',$3)`, id, state, cause.Error()); err != nil {
		logger.Error("job error event insert failed", "error", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		logger.Error("job error commit failed", "error", err)
		return
	}
	if executor != nil {
		if removeErr := executor.RemoveIfExists(ctx, containerName(appID, releaseID)); removeErr != nil {
			logger.Warn("failed deployment container cleanup failed", "job", id, "error", removeErr)
		}
	}
}
