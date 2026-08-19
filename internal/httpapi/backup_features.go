package httpapi

import (
	"math"
	"math/big"
	"net/http"
	"strings"
	"time"

	runtimepolicy "cloudmeter/internal/runtime"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listAppBackups(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var runtimeSpec map[string]any
	err := s.db.QueryRow(r.Context(), `SELECT coalesce(rel.immutable_snapshot->'runtime_spec','{}'::jsonb) FROM user_apps a LEFT JOIN app_releases rel ON rel.id=a.last_successful_release_id WHERE a.id=$1 AND a.user_id=$2`, appID, p.ID).Scan(&runtimeSpec)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	capacityGiB, _ := runtimepolicy.RuntimeDataVolumeGiB(runtimeSpec, false)
	volumes := runtimepolicy.VolumeMounts(runtimeSpec)
	activeVolumeKeys := make(map[string]bool, len(volumes))
	for _, volume := range volumes {
		activeVolumeKeys[volume.Key] = true
	}
	rows, err := s.db.Query(r.Context(), `SELECT b.id,b.volume_key,b.status,b.size_bytes,b.last_error,b.created_at,b.completed_at,coalesce(d.status,'')
		FROM app_backups b JOIN user_apps a ON a.id=b.user_app_id
		LEFT JOIN app_backup_deletion_jobs d ON d.backup_id=b.id
		WHERE b.user_app_id=$1 AND a.user_id=$2 AND coalesce(d.status,'') <> 'succeeded'
		ORDER BY b.volume_key,b.created_at DESC LIMIT 100`, appID, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, key, status string
		var size *int64
		var lastError *string
		var created time.Time
		var completed *time.Time
		var deletionStatus string
		if err := rows.Scan(&id, &key, &status, &size, &lastError, &created, &completed, &deletionStatus); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "volumeKey": key, "status": status, "sizeBytes": size, "lastError": lastError, "createdAt": created, "completedAt": completed, "deletionStatus": deletionStatus})
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	volumeUsage := map[string]int64{}
	metricRows, metricErr := s.db.Query(r.Context(), `SELECT volume_key,usage_bytes FROM app_storage_metrics WHERE user_app_id=$1 AND sampled_at>=now()-interval '15 minutes'`, appID)
	if metricErr != nil {
		s.internalError(w, metricErr)
		return
	}
	for metricRows.Next() {
		var key string
		var bytes int64
		if metricErr = metricRows.Scan(&key, &bytes); metricErr != nil {
			metricRows.Close()
			s.internalError(w, metricErr)
			return
		}
		if activeVolumeKeys[key] {
			volumeUsage[key] = bytes
		}
	}
	metricRows.Close()
	if metricErr = metricRows.Err(); metricErr != nil {
		s.internalError(w, metricErr)
		return
	}
	var backupUsage int64
	if err = s.db.QueryRow(r.Context(), `SELECT coalesce(sum(backup.size_bytes),0)::bigint FROM app_backups backup
		LEFT JOIN app_backup_deletion_jobs deletion ON deletion.backup_id=backup.id
		WHERE backup.user_app_id=$1 AND backup.status='succeeded' AND coalesce(deletion.status,'') <> 'succeeded'`, appID).Scan(&backupUsage); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backups": items, "volumes": volumes, "capacityGiB": capacityGiB, "volumeUsageBytes": volumeUsage, "backupUsageBytes": backupUsage})
}

// deleteAppBackup queues physical archive cleanup while retaining the
// immutable app_backups row for audit/history. A failed cleanup can be
// submitted again and will reuse the same deletion job.
func (s *Server) deleteAppBackup(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID, backupID := r.PathValue("appID"), r.PathValue("backupID")
	if !validUUID(appID) || !validUUID(backupID) {
		writeError(w, http.StatusNotFound, "backup_not_found", "backup not found")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var backupStatus, existingDeletion string
	err = tx.QueryRow(r.Context(), `SELECT b.status,coalesce(d.status,'')
		FROM app_backups b JOIN user_apps a ON a.id=b.user_app_id
		LEFT JOIN app_backup_deletion_jobs d ON d.backup_id=b.id
		WHERE b.id=$1 AND b.user_app_id=$2 AND a.user_id=$3 FOR UPDATE OF b`, backupID, appID, p.ID).Scan(&backupStatus, &existingDeletion)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "backup_not_found", "backup not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if backupStatus == "queued" || backupStatus == "running" {
		writeError(w, http.StatusConflict, "backup_in_progress", "正在创建的备份不能删除，请等待任务完成")
		return
	}
	var restoring bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_restore_jobs WHERE backup_id=$1 AND status IN ('queued','running'))`, backupID).Scan(&restoring); err != nil {
		s.internalError(w, err)
		return
	}
	if restoring {
		writeError(w, http.StatusConflict, "restore_in_progress", "备份正在恢复，完成后才能删除")
		return
	}
	if existingDeletion == "succeeded" {
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"backupId": backupID, "status": "succeeded", "idempotent": true})
		return
	}
	var jobID string
	if existingDeletion == "failed" {
		if err = tx.QueryRow(r.Context(), `UPDATE app_backup_deletion_jobs SET status='queued',attempts=0,available_at=now(),updated_at=now(),last_error=NULL,completed_at=NULL WHERE backup_id=$1 RETURNING id`, backupID).Scan(&jobID); err != nil {
			s.internalError(w, err)
			return
		}
	} else if existingDeletion == "queued" || existingDeletion == "running" {
		if err = tx.QueryRow(r.Context(), `SELECT id::text FROM app_backup_deletion_jobs WHERE backup_id=$1`, backupID).Scan(&jobID); err != nil {
			s.internalError(w, err)
			return
		}
	} else {
		if err = tx.QueryRow(r.Context(), `INSERT INTO app_backup_deletion_jobs(backup_id,requested_by) VALUES($1,$2) RETURNING id::text`, backupID, p.ID).Scan(&jobID); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$2,'backup.delete_requested','app_backup',$3,$4,jsonb_build_object('deletion_job_id',$5::text))`, p.auditActorID(), p.ID, backupID, requestID(r.Context()), jobID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"backupId": backupID, "deletionJobId": jobID, "status": "queued"})
}

func (s *Server) createAppBackup(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var q struct {
		VolumeKey string `json:"volumeKey"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.VolumeKey = strings.TrimSpace(q.VolumeKey)
	if q.VolumeKey == "" {
		writeError(w, 400, "validation_failed", "volumeKey is required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var runtimeSpec map[string]any
	err = tx.QueryRow(r.Context(), `SELECT r.immutable_snapshot->'runtime_spec' FROM user_apps a JOIN app_releases r ON r.id=a.last_successful_release_id WHERE a.id=$1 AND a.user_id=$2 AND a.status='running' FOR UPDATE OF a`, appID, p.ID).Scan(&runtimeSpec)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "app_not_running", "running application not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	found := false
	reservedBytes := int64(0)
	for _, mount := range runtimepolicy.VolumeMounts(runtimeSpec) {
		if mount.Key == q.VolumeKey {
			found = true
			reservedBytes = int64(math.Ceil(mount.SizeGiB * 1024 * 1024 * 1024))
			break
		}
	}
	if !found {
		writeError(w, 400, "volume_not_found", "volume is not declared by the active release")
		return
	}
	var active bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_backups WHERE user_app_id=$1 AND status IN ('queued','running'))`, appID).Scan(&active); err != nil {
		s.internalError(w, err)
		return
	}
	if active {
		writeError(w, 409, "backup_in_progress", "this application already has a backup in progress")
		return
	}
	var id, storageKey string
	dockerVolume := runtimepolicy.AppVolumeNameForOwner(s.cfg.RuntimeOwner, appID, q.VolumeKey)
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_backups(user_app_id,volume_key,docker_volume,storage_key,reserved_bytes) VALUES($1,$2,$3,gen_random_uuid()::text||'.tar.gz',$4) RETURNING id,storage_key`, appID, q.VolumeKey, dockerVolume, reservedBytes).Scan(&id, &storageKey); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"backupId": id, "status": "queued"})
}

func backupStorageQuotaExceeded(limitGiB string, usedBytes, reservedBytes int64) bool {
	limit, ok := new(big.Rat).SetString(strings.TrimSpace(limitGiB))
	if !ok || limit.Sign() < 0 || usedBytes < 0 || reservedBytes < 0 {
		return true
	}
	totalBytes := new(big.Int).Add(big.NewInt(usedBytes), big.NewInt(reservedBytes))
	total := new(big.Rat).SetFrac(totalBytes, big.NewInt(1<<30))
	return total.Cmp(limit) > 0
}

func (s *Server) restoreAppBackup(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID, backupID := r.PathValue("appID"), r.PathValue("backupID")
	var q struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.IdempotencyKey = strings.TrimSpace(q.IdempotencyKey)
	if q.IdempotencyKey == "" || len(q.IdempotencyKey) > 128 {
		writeError(w, 400, "validation_failed", "idempotencyKey is required and must be at most 128 characters")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var existingID, existingStatus, existingBackupID string
	if err = tx.QueryRow(r.Context(), `SELECT j.id,j.status,j.backup_id FROM app_restore_jobs j JOIN user_apps a ON a.id=j.user_app_id WHERE j.user_app_id=$1 AND j.idempotency_key=$2 AND a.user_id=$3`, appID, q.IdempotencyKey, p.ID).Scan(&existingID, &existingStatus, &existingBackupID); err == nil {
		if existingBackupID != backupID {
			writeError(w, 409, "idempotency_conflict", "idempotency key is already used for a different backup")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"restoreJobId": existingID, "status": existingStatus})
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var lockedBackupID string
	if err = tx.QueryRow(r.Context(), `SELECT b.id::text FROM app_backups b JOIN user_apps a ON a.id=b.user_app_id
		LEFT JOIN app_backup_deletion_jobs deletion ON deletion.backup_id=b.id
		WHERE b.id=$1 AND b.user_app_id=$2 AND b.status='succeeded' AND a.user_id=$3 AND a.status='running'
		  AND coalesce(deletion.status,'') NOT IN ('queued','running','succeeded') FOR UPDATE OF b`, backupID, appID, p.ID).Scan(&lockedBackupID); err == pgx.ErrNoRows {
		writeError(w, 404, "backup_not_restorable", "successful backup not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	// Serialize restore creation per application so two concurrent requests
	// cannot both pass the active-job check.
	var lockedAppID string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM user_apps WHERE id=$1 AND user_id=$2 AND status='running' FOR UPDATE`, appID, p.ID).Scan(&lockedAppID); err == pgx.ErrNoRows {
		writeError(w, 404, "app_not_running", "running application not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	var active bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_restore_jobs WHERE user_app_id=$1 AND status IN ('queued','running'))`, appID).Scan(&active); err != nil {
		s.internalError(w, err)
		return
	}
	if active {
		writeError(w, 409, "restore_in_progress", "a restore is already in progress")
		return
	}
	var id string
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_restore_jobs(backup_id,user_app_id,idempotency_key) VALUES($1,$2,$3) RETURNING id`, backupID, appID, q.IdempotencyKey).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE user_apps SET status='updating' WHERE id=$1`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"restoreJobId": id, "status": "queued"})
}
