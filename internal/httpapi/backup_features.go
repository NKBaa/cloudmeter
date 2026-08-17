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
	rows, err := s.db.Query(r.Context(), `SELECT b.id,b.volume_key,b.status,b.size_bytes,b.last_error,b.created_at,b.completed_at FROM app_backups b JOIN user_apps a ON a.id=b.user_app_id WHERE b.user_app_id=$1 AND a.user_id=$2 ORDER BY b.created_at DESC LIMIT 100`, appID, p.ID)
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
		if err := rows.Scan(&id, &key, &status, &size, &lastError, &created, &completed); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "volumeKey": key, "status": status, "sizeBytes": size, "lastError": lastError, "createdAt": created, "completedAt": completed})
	}
	writeJSON(w, 200, map[string]any{"backups": items, "volumes": runtimepolicy.VolumeMounts(runtimeSpec)})
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
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_backups WHERE user_app_id=$1 AND volume_key=$2 AND status IN ('queued','running'))`, appID, q.VolumeKey).Scan(&active); err != nil {
		s.internalError(w, err)
		return
	}
	if active {
		writeError(w, 409, "backup_in_progress", "a backup is already in progress")
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
	var valid bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_backups b JOIN user_apps a ON a.id=b.user_app_id WHERE b.id=$1 AND b.user_app_id=$2 AND b.status='succeeded' AND a.user_id=$3 AND a.status='running')`, backupID, appID, p.ID).Scan(&valid); err != nil {
		s.internalError(w, err)
		return
	}
	if !valid {
		writeError(w, 404, "backup_not_restorable", "successful backup not found")
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
