package httpapi

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	runtimepolicy "cloudmeter/internal/runtime"
	"github.com/jackc/pgx/v5"
)

const maxBackupImportBytes int64 = 2 << 30
const maxBackupExpandedBytes int64 = 20 << 30

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

func (s *Server) exportAppBackup(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID, backupID := r.PathValue("appID"), r.PathValue("backupID")
	if !validUUID(appID) || !validUUID(backupID) {
		writeError(w, http.StatusNotFound, "backup_not_found", "backup not found")
		return
	}
	var storageKey, appSlug, volumeKey string
	var createdAt time.Time
	err := s.db.QueryRow(r.Context(), `SELECT backup.storage_key,app.slug,backup.volume_key,backup.created_at
		FROM app_backups backup JOIN user_apps app ON app.id=backup.user_app_id
		LEFT JOIN app_backup_deletion_jobs deletion ON deletion.backup_id=backup.id
		WHERE backup.id=$1 AND backup.user_app_id=$2 AND app.user_id=$3 AND backup.status='succeeded'
		  AND coalesce(deletion.status,'') NOT IN ('queued','running','succeeded')`, backupID, appID, p.ID).
		Scan(&storageKey, &appSlug, &volumeKey, &createdAt)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "backup_not_found", "successful backup not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	archivePath, err := s.backupArchivePath(storageKey)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = s.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'backup.export','app_backup',$3,$4,jsonb_build_object('app_id',$5::text,'volume_key',$6::text))`, p.auditActorID(), p.ID, backupID, requestID(r.Context()), appID, volumeKey); err != nil {
		s.internalError(w, err)
		return
	}
	file, err := os.Open(archivePath)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "backup_archive_missing", "backup archive is missing")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.internalError(w, err)
		return
	}
	filename := fmt.Sprintf("%s-%s-%s.tar.gz", appSlug, volumeKey, createdAt.UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	if _, err = io.Copy(w, file); err != nil {
		s.logger.Warn("backup export interrupted", "backup", backupID, "error", err)
	}
}

func (s *Server) importAppBackup(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	if !validUUID(appID) {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupImportBytes+1<<20)
	if err := os.MkdirAll(s.cfg.BackupStoragePath, 0o770); err != nil {
		s.internalError(w, err)
		return
	}
	temp, err := os.CreateTemp(s.cfg.BackupStoragePath, ".import-*.tar.gz")
	if err != nil {
		s.internalError(w, err)
		return
	}
	tempPath := temp.Name()
	keepArchive := false
	defer func() {
		temp.Close()
		if !keepArchive {
			_ = os.Remove(tempPath)
		}
	}()
	multipartReader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup_archive", "请求必须使用 multipart/form-data")
		return
	}
	volumeKey := ""
	var size int64
	fileFound := false
	for {
		part, partErr := multipartReader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_backup_archive", "无法读取上传文件")
			return
		}
		switch part.FormName() {
		case "volumeKey":
			value, readErr := io.ReadAll(io.LimitReader(part, 1025))
			if readErr != nil || len(value) > 1024 {
				part.Close()
				writeError(w, http.StatusBadRequest, "validation_failed", "数据卷标识无效")
				return
			}
			volumeKey = strings.TrimSpace(string(value))
		case "file":
			if fileFound {
				part.Close()
				writeError(w, http.StatusBadRequest, "validation_failed", "一次只能导入一个备份文件")
				return
			}
			fileFound = true
			size, err = io.Copy(temp, io.LimitReader(part, maxBackupImportBytes+1))
		}
		part.Close()
		if err != nil {
			break
		}
	}
	if volumeKey == "" || !fileFound {
		writeError(w, http.StatusBadRequest, "validation_failed", "必须选择数据卷和 tar.gz 备份文件")
		return
	}
	if err != nil || size == 0 || size > maxBackupImportBytes {
		writeError(w, http.StatusBadRequest, "invalid_backup_archive", "备份文件必须为非空且不超过 2 GiB")
		return
	}
	if err = temp.Sync(); err != nil {
		s.internalError(w, err)
		return
	}
	if err = temp.Close(); err != nil {
		s.internalError(w, err)
		return
	}
	if err = validateBackupArchive(tempPath); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup_archive", err.Error())
		return
	}

	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var runtimeSpec map[string]any
	err = tx.QueryRow(r.Context(), `SELECT release.immutable_snapshot->'runtime_spec' FROM user_apps app
		JOIN app_releases release ON release.id=app.last_successful_release_id
		WHERE app.id=$1 AND app.user_id=$2 FOR UPDATE OF app`, appID, p.ID).Scan(&runtimeSpec)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	volumeFound := false
	for _, mount := range runtimepolicy.VolumeMounts(runtimeSpec) {
		if mount.Key == volumeKey {
			volumeFound = true
			break
		}
	}
	if !volumeFound {
		writeError(w, http.StatusBadRequest, "volume_not_found", "volume is not declared by the active release")
		return
	}
	var activeBackup bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_backups WHERE user_app_id=$1 AND status IN ('queued','running'))`, appID).Scan(&activeBackup); err != nil {
		s.internalError(w, err)
		return
	}
	if activeBackup {
		writeError(w, http.StatusConflict, "backup_in_progress", "应用已有备份任务，完成后才能导入归档")
		return
	}
	capacityGiB, _ := runtimepolicy.RuntimeDataVolumeGiB(runtimeSpec, false)
	var liveBytes, retainedBytes int64
	if err = tx.QueryRow(r.Context(), `SELECT coalesce((SELECT sum(usage_bytes) FROM (
		SELECT DISTINCT ON (volume_key) volume_key,usage_bytes FROM app_storage_metrics
		WHERE user_app_id=$1 AND sampled_at>=now()-interval '15 minutes' ORDER BY volume_key,sampled_at DESC
	) latest_metrics),0)::bigint,
		coalesce((SELECT sum(backup.size_bytes) FROM app_backups backup LEFT JOIN app_backup_deletion_jobs deletion ON deletion.backup_id=backup.id
		WHERE backup.user_app_id=$1 AND backup.status='succeeded' AND coalesce(deletion.status,'') <> 'succeeded'),0)::bigint`, appID).Scan(&liveBytes, &retainedBytes); err != nil {
		s.internalError(w, err)
		return
	}
	if backupStorageQuotaExceeded(fmt.Sprintf("%g", capacityGiB), liveBytes+retainedBytes, size) {
		writeError(w, http.StatusConflict, "backup_capacity_exceeded", "导入归档会超过应用共享数据卷容量，请先删除旧备份或扩容")
		return
	}
	var backupID, storageKey string
	dockerVolume := runtimepolicy.AppVolumeNameForOwner(s.cfg.RuntimeOwner, appID, volumeKey)
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_backups(user_app_id,volume_key,docker_volume,storage_key,reserved_bytes)
		VALUES($1,$2,$3,gen_random_uuid()::text||'.tar.gz',0) RETURNING id::text,storage_key`, appID, volumeKey, dockerVolume).Scan(&backupID, &storageKey); err != nil {
		s.internalError(w, err)
		return
	}
	archivePath, err := s.backupArchivePath(storageKey)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err = os.Rename(tempPath, archivePath); err != nil {
		s.internalError(w, err)
		return
	}
	tempPath = archivePath
	if _, err = tx.Exec(r.Context(), `UPDATE app_backups SET status='running' WHERE id=$1`, backupID); err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE app_backups SET status='succeeded',size_bytes=$2,completed_at=now(),last_error=NULL WHERE id=$1`, backupID, size)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
			VALUES($1,$2,'backup.import','app_backup',$3,$4,jsonb_build_object('app_id',$5::text,'volume_key',$6::text,'size_bytes',$7::bigint))`, p.auditActorID(), p.ID, backupID, requestID(r.Context()), appID, volumeKey, size)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	keepArchive = true
	writeJSON(w, http.StatusCreated, map[string]any{"backupId": backupID, "status": "succeeded", "sizeBytes": size})
}

func (s *Server) backupArchivePath(storageKey string) (string, error) {
	if storageKey == "" || filepath.Base(storageKey) != storageKey || !strings.HasSuffix(storageKey, ".tar.gz") {
		return "", fmt.Errorf("invalid backup storage key")
	}
	return filepath.Join(s.cfg.BackupStoragePath, storageKey), nil
}

func validateBackupArchive(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("文件不是有效的 tar.gz 归档")
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	entries := 0
	var expandedBytes int64
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("tar.gz 归档已损坏")
		}
		entries++
		if entries > 1_000_000 || header.Size < 0 || expandedBytes > maxBackupExpandedBytes-header.Size {
			return fmt.Errorf("归档解压后的数据过大或文件数量过多")
		}
		expandedBytes += header.Size
		clean := path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))
		if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("归档包含不安全的文件路径")
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		default:
			return fmt.Errorf("归档包含不支持的链接或设备文件")
		}
	}
	if entries == 0 {
		return fmt.Errorf("归档中没有可恢复的数据")
	}
	return nil
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
