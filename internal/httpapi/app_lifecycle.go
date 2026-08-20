package httpapi

import (
	runtimepolicy "cloudmeter/internal/runtime"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math"
	"net/http"
	"strings"
	"time"
)

func (s *Server) appReleases(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	rows, err := s.db.Query(r.Context(), `SELECT r.id,r.release_number,r.product_version_id,r.state,r.immutable_snapshot,r.created_at,coalesce(j.state,''),coalesce(j.id::text,'') FROM app_releases r JOIN user_apps a ON a.id=r.user_app_id LEFT JOIN LATERAL (SELECT id,state FROM deployment_jobs WHERE release_id=r.id AND user_app_id=r.user_app_id ORDER BY created_at DESC LIMIT 1) j ON true WHERE r.user_app_id=$1 AND a.user_id=$2 ORDER BY r.release_number DESC`, appID, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type release struct {
		ID               string         `json:"id"`
		ReleaseNumber    int            `json:"releaseNumber"`
		ProductVersionID string         `json:"productVersionId"`
		State            string         `json:"state"`
		Snapshot         map[string]any `json:"snapshot"`
		CreatedAt        time.Time      `json:"createdAt"`
		JobState         string         `json:"jobState"`
		JobID            string         `json:"jobId"`
	}
	items := []release{}
	for rows.Next() {
		var item release
		if err := rows.Scan(&item.ID, &item.ReleaseNumber, &item.ProductVersionID, &item.State, &item.Snapshot, &item.CreatedAt, &item.JobState, &item.JobID); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"releases": items})
}

// appVersions lists the versions that the owner may select for a new release.
// Runtime and routing declarations are returned so the client can explain the
// resulting configuration, but Secret values never live in these records and
// are never read by this endpoint.
func (s *Server) appVersions(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var currentVersionID string
	if err := s.db.QueryRow(r.Context(), `SELECT coalesce(release.product_version_id::text,'')
		FROM user_apps app
		LEFT JOIN app_releases release ON release.id=app.last_successful_release_id
		WHERE app.id=$1 AND app.user_id=$2 AND app.deleted_at IS NULL`, appID, p.ID).Scan(&currentVersionID); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "app_not_found", "application not found")
			return
		}
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT version.id::text,version.version,version.version_label,
		version.runtime_spec,version.route_spec,version.health_spec,version.update_spec,version.published_at
		FROM user_apps app
		JOIN app_product_versions version ON version.product_id=app.product_id
		WHERE app.id=$1 AND app.user_id=$2 AND app.deleted_at IS NULL
		  AND version.published_at IS NOT NULL AND version.archived_at IS NULL
		ORDER BY version.version DESC`, appID, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, label string
		var number int
		var runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any
		var publishedAt time.Time
		if err = rows.Scan(&id, &number, &label, &runtimeSpec, &routeSpec, &healthSpec, &updateSpec, &publishedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "version": number, "versionLabel": label, "runtimeSpec": runtimeSpec,
			"routeSpec": routeSpec, "healthSpec": healthSpec, "updateSpec": updateSpec,
			"publishedAt": publishedAt, "current": id == currentVersionID,
		})
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"versions": items, "currentVersionId": currentVersionID})
}

// appConfiguration returns the active executable settings together with the
// latest published product template. Secret values are deliberately omitted;
// only declared/configured key names are returned.
func (s *Server) appConfiguration(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var appSlug, appStatus, productID, productSlug, productName, iconURL string
	var currentVersionID, targetVersionID string
	var targetVersion int
	var currentRuntime, currentRoute, targetRuntime, targetRoute map[string]any
	err := s.db.QueryRow(r.Context(), `SELECT app.slug,app.status,product.id::text,product.slug,product.name,product.icon_url,
		current_release.product_version_id::text,current_release.immutable_snapshot->'runtime_spec',current_release.immutable_snapshot->'route_spec',
		coalesce(target.id,current_version.id)::text,coalesce(target.version,current_version.version),
		coalesce(target.runtime_spec,current_release.immutable_snapshot->'runtime_spec'),
		coalesce(target.route_spec,current_release.immutable_snapshot->'route_spec')
		FROM user_apps app
		JOIN app_products product ON product.id=app.product_id
		JOIN app_releases current_release ON current_release.id=app.last_successful_release_id
		JOIN app_product_versions current_version ON current_version.id=current_release.product_version_id
		LEFT JOIN LATERAL (SELECT id,version,runtime_spec,route_spec FROM app_product_versions
			WHERE product_id=product.id AND published_at IS NOT NULL AND archived_at IS NULL ORDER BY version DESC LIMIT 1) target ON true
		WHERE app.id=$1 AND app.user_id=$2 AND app.deleted_at IS NULL`, appID, p.ID).Scan(
		&appSlug, &appStatus, &productID, &productSlug, &productName, &iconURL,
		&currentVersionID, &currentRuntime, &currentRoute, &targetVersionID, &targetVersion, &targetRuntime, &targetRoute,
	)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_configuration_not_found", "application has no successful configuration")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT secret.key FROM app_secrets secret
		WHERE secret.user_app_id=$1 AND EXISTS(SELECT 1 FROM app_secret_versions version WHERE version.app_secret_id=secret.id)
		ORDER BY secret.key`, appID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	configuredKeys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			s.internalError(w, err)
			return
		}
		configuredKeys = append(configuredKeys, key)
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"app":                  map[string]any{"id": appID, "slug": appSlug, "status": appStatus, "productSlug": productSlug},
		"current":              map[string]any{"versionId": currentVersionID, "runtimeSpec": currentRuntime, "routeSpec": currentRoute},
		"target":               map[string]any{"productId": productID, "productSlug": productSlug, "name": productName, "iconUrl": iconURL, "versionId": targetVersionID, "version": targetVersion, "runtimeSpec": targetRuntime, "routeSpec": targetRoute},
		"configuredSecretKeys": configuredKeys,
	})
}

func (s *Server) appRoute(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var path, host string
	var port int
	var releaseID string
	err := s.db.QueryRow(r.Context(), `SELECT ar.public_path,ar.upstream_host,ar.upstream_port,ar.release_id::text FROM app_routes ar JOIN user_apps a ON a.id=ar.user_app_id WHERE ar.user_app_id=$1 AND a.user_id=$2`, r.PathValue("appID"), p.ID).Scan(&path, &host, &port, &releaseID)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "route_not_found", "application route is not active")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"publicPath": path, "upstreamHost": host, "upstreamPort": port, "releaseId": releaseID})
}

type appReleaseRequest struct {
	VersionID      string             `json:"versionId"`
	IdempotencyKey string             `json:"idempotencyKey"`
	Resources      *selectedResources `json:"resources"`
	Secrets        map[string]string  `json:"secrets"`
}

func (s *Server) createAppRelease(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var req appReleaseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	req.VersionID = strings.TrimSpace(req.VersionID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if !validUUID(req.VersionID) || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 {
		writeError(w, 400, "validation_failed", "versionId and idempotencyKey are required")
		return
	}
	s.createReleaseJob(w, r, p.auditActorID(), p.ID, appID, req.VersionID, req.IdempotencyKey, "", "update", req.Resources, req.Secrets)
}
func (s *Server) rollbackApp(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := r.PathValue("appID")
	var q struct {
		ReleaseID      string `json:"releaseId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.ReleaseID = strings.TrimSpace(q.ReleaseID)
	q.IdempotencyKey = strings.TrimSpace(q.IdempotencyKey)
	if !validUUID(q.ReleaseID) || q.IdempotencyKey == "" || len(q.IdempotencyKey) > 128 {
		writeError(w, 400, "validation_failed", "releaseId and idempotencyKey are required")
		return
	}
	var versionID string
	if err := s.db.QueryRow(r.Context(), "SELECT product_version_id FROM app_releases WHERE id=$1 AND user_app_id=$2 AND state IN ('active','superseded')", q.ReleaseID, appID).Scan(&versionID); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 404, "release_not_found", "release not found")
			return
		}
		s.internalError(w, err)
		return
	}
	s.createReleaseJob(w, r, p.auditActorID(), p.ID, appID, versionID, q.IdempotencyKey, q.ReleaseID, "rollback", nil, nil)
}
func (s *Server) createReleaseJob(w http.ResponseWriter, r *http.Request, actorID, userID, appID, versionID, key, rollbackReleaseID, operation string, selected *selectedResources, providedSecrets map[string]string) {
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var existingJob, existingApp, existingOperation, existingVersion, existingSource string
	if err = tx.QueryRow(r.Context(), `SELECT j.id,j.user_app_id,j.operation,r.product_version_id::text,coalesce(j.source_release_id::text,'')
		FROM deployment_jobs j JOIN user_apps a ON a.id=j.user_app_id JOIN app_releases r ON r.id=j.release_id AND r.user_app_id=j.user_app_id
		WHERE j.idempotency_key=$1 AND a.user_id=$2`, key, userID).Scan(&existingJob, &existingApp, &existingOperation, &existingVersion, &existingSource); err == nil {
		if existingApp != appID || existingOperation != operation || existingVersion != versionID || existingSource != rollbackReleaseID {
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key is already used for another application operation")
			return
		}
		_ = tx.Commit(r.Context())
		writeJSON(w, 200, map[string]any{"appId": existingApp, "jobId": existingJob, "idempotent": true})
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	if err = enforceDeploymentConcurrency(r.Context(), tx, userID); err != nil {
		if quota, ok := err.(resourceQuotaError); ok {
			writeError(w, 409, quota.Code, quota.Message)
			return
		}
		s.internalError(w, err)
		return
	}
	var currentSnapshot map[string]any
	var currentReleaseCreated *time.Time
	var appStatus, suspensionReason string
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(rel.immutable_snapshot,'{}'::jsonb),rel.created_at,app.status,coalesce(app.suspension_reason,'')
		FROM user_apps app LEFT JOIN app_releases rel ON rel.id=app.last_successful_release_id
		WHERE app.id=$1 AND app.user_id=$2 FOR UPDATE OF app`, appID, userID).Scan(&currentSnapshot, &currentReleaseCreated, &appStatus, &suspensionReason); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, 404, "app_not_found", "application not found")
			return
		}
		s.internalError(w, err)
		return
	}
	if appStatus == "suspended" {
		writeError(w, http.StatusConflict, "app_suspended", "application is suspended by platform policy: "+suspensionReason)
		return
	}
	if appStatus != "running" {
		writeError(w, http.StatusConflict, "app_not_running", "only a running application can be updated or rolled back")
		return
	}
	var activeJobID string
	if err = tx.QueryRow(r.Context(), `SELECT j.id FROM deployment_jobs j JOIN user_apps a ON a.id=j.user_app_id WHERE j.user_app_id=$1 AND a.user_id=$2 AND j.state NOT IN ('succeeded','failed') ORDER BY j.created_at DESC LIMIT 1 FOR UPDATE`, appID, userID).Scan(&activeJobID); err == nil {
		writeError(w, 409, "deployment_in_progress", "application already has an active deployment")
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var snapshot map[string]any
	if rollbackReleaseID != "" {
		if err = tx.QueryRow(r.Context(), `SELECT r.immutable_snapshot FROM app_releases r JOIN user_apps a ON a.id=r.user_app_id WHERE r.id=$1 AND r.user_app_id=$2 AND a.user_id=$3 AND r.state IN ('active','superseded')`, rollbackReleaseID, appID, userID).Scan(&snapshot); err != nil {
			if err == pgx.ErrNoRows {
				writeError(w, 404, "release_not_found", "release not found")
				return
			}
			s.internalError(w, err)
			return
		}
	} else {
		var productSlug, imageDigest string
		var runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any
		if err = tx.QueryRow(r.Context(), `SELECT p.slug,pv.image_digest,pv.runtime_spec,pv.route_spec,pv.health_spec,pv.update_spec FROM user_apps a JOIN app_products p ON p.id=a.product_id JOIN app_product_versions pv ON pv.product_id=p.id WHERE a.id=$1 AND a.user_id=$2 AND pv.id=$3 AND pv.published_at IS NOT NULL AND pv.archived_at IS NULL`, appID, userID, versionID).Scan(&productSlug, &imageDigest, &runtimeSpec, &routeSpec, &healthSpec, &updateSpec); err != nil {
			if err == pgx.ErrNoRows {
				writeError(w, 404, "version_not_found", "published version not found")
				return
			}
			s.internalError(w, err)
			return
		}
		if operation == "update" {
			runtimeSpec, err = runtimeSpecForUpdate(runtimeSpec, snapshotRuntime(currentSnapshot), selected)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_deployment_configuration", err.Error())
				return
			}
			if err = s.applyReleaseSecretUpdates(r.Context(), tx, appID, runtimeSpec, providedSecrets); err != nil {
				if validation, ok := err.(*secretValidationError); ok {
					writeError(w, validation.Status, validation.Code, validation.Message)
				} else {
					s.internalError(w, err)
				}
				return
			}
		}
		if err = tx.QueryRow(r.Context(), `SELECT jsonb_build_object('product_slug',$2::text,'image_digest',$3::text,'runtime_spec',$4::jsonb,'route_spec',$5::jsonb,'health_spec',$6::jsonb,'update_spec',$7::jsonb,'secret_versions',coalesce((SELECT jsonb_object_agg(s.key,v.id::text) FROM app_secrets s JOIN LATERAL (SELECT id FROM app_secret_versions WHERE app_secret_id=s.id ORDER BY version DESC LIMIT 1) v ON true WHERE s.user_app_id=$1 AND s.key IN (SELECT jsonb_array_elements_text(coalesce($4::jsonb->'secretKeys','[]'::jsonb)))),'{}'::jsonb))`, appID, productSlug, imageDigest, runtimeSpec, routeSpec, healthSpec, updateSpec).Scan(&snapshot); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if currentReleaseCreated != nil && (snapshotDataPolicy(currentSnapshot) == "backup_required" || snapshotDataPolicy(snapshot) == "backup_required") {
		missing, backupErr := missingRequiredBackups(r.Context(), tx, appID, snapshotRuntime(currentSnapshot), *currentReleaseCreated)
		if backupErr != nil {
			s.internalError(w, backupErr)
			return
		}
		if len(missing) > 0 {
			writeError(w, 409, "backup_required_before_update", "successful backups are required for volumes: "+strings.Join(missing, ", "))
			return
		}
	}
	if missing, dependencyErr := missingRuntimeDependencies(r.Context(), tx, userID, appID, snapshotRuntime(snapshot)); dependencyErr != nil {
		s.internalError(w, dependencyErr)
		return
	} else if len(missing) > 0 {
		writeError(w, 409, "required_dependency_unavailable", "required dependencies are not running: "+strings.Join(dependencyLabels(missing), ", "))
		return
	}
	if missing, secretErr := missingSnapshotSecrets(snapshot); secretErr != nil {
		s.internalError(w, secretErr)
		return
	} else if len(missing) > 0 {
		writeError(w, 409, "required_secret_missing", "set required application Secrets before updating: "+strings.Join(missing, ", "))
		return
	}
	if err = enforceRuntimeEntitlements(r.Context(), tx, userID, appID, snapshotRuntime(snapshot)); err != nil {
		if quota, ok := err.(resourceQuotaError); ok {
			writeError(w, 409, quota.Code, quota.Message)
			return
		}
		s.internalError(w, err)
		return
	}
	var releaseID string
	var lastReleaseNumber int
	err = tx.QueryRow(r.Context(), "SELECT release_number FROM app_releases WHERE user_app_id=$1 ORDER BY release_number DESC LIMIT 1 FOR UPDATE", appID).Scan(&lastReleaseNumber)
	if err != nil && err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	releaseNumber := lastReleaseNumber + 1
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_releases(user_app_id,product_version_id,release_number,immutable_snapshot,state) VALUES($1,$2,$3,$4,'created') RETURNING id`, appID, versionID, releaseNumber, snapshot).Scan(&releaseID); err != nil {
		s.internalError(w, err)
		return
	}
	var jobID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO deployment_jobs(user_app_id,release_id,idempotency_key,operation,source_release_id)
		VALUES($1,$2,$3,$4,nullif($5,'')::uuid) RETURNING id`, appID, releaseID, key, operation, rollbackReleaseID).Scan(&jobID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE user_apps SET status='updating' WHERE id=$1", appID); err != nil {
		s.internalError(w, err)
		return
	}
	action := "app." + operation
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,$2::uuid,$3,'user_app',$4,$5,jsonb_build_object('job_id',$6::text,'release_id',$7::text,'product_version_id',$8::text,'rollback_source_release_id',nullif($9::text,'')))`, actorID, userID, action, appID, requestID(r.Context()), jobID, releaseID, versionID, rollbackReleaseID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"appId": appID, "releaseId": releaseID, "jobId": jobID, "status": "updating", "operation": operation})
}

// runtimeSpecForUpdate overlays user-editable values from the active release
// onto the latest immutable product template. Omitted values stay unchanged;
// fixed template fields always come from the administrator's new version.
func runtimeSpecForUpdate(template, current map[string]any, requested *selectedResources) (map[string]any, error) {
	selection := selectedResources{}
	if requested != nil {
		selection = *requested
	}
	minimumCompute, minimumErr := runtimepolicy.RuntimeResources(template, true)
	if minimumErr != nil {
		return nil, minimumErr
	}
	if editableRuntimeOption(template, "cpu", true) && selection.CPUCores == nil {
		if resources, err := runtimepolicy.RuntimeResources(current, false); err == nil {
			value := math.Max(resources.CPUCores, minimumCompute.CPUCores)
			selection.CPUCores = &value
		}
	}
	if editableRuntimeOption(template, "memory", true) && selection.MemoryMiB == nil {
		if resources, err := runtimepolicy.RuntimeResources(current, false); err == nil {
			value := math.Max(resources.MemoryMiB, minimumCompute.MemoryMiB)
			selection.MemoryMiB = &value
		}
	}
	targetVolumes := runtimepolicy.VolumeMounts(template)
	currentCapacity, currentErr := runtimepolicy.RuntimeDataVolumeGiB(current, false)
	if currentErr != nil {
		return nil, currentErr
	}
	minimumCapacity, minimumCapacityErr := runtimepolicy.RuntimeDataVolumeGiB(template, false)
	if minimumCapacityErr != nil {
		return nil, minimumCapacityErr
	}
	if currentCapacity > 0 && len(targetVolumes) == 0 {
		return nil, fmt.Errorf("the latest product version removes the existing shared data volume; data volumes can only be expanded")
	}
	if currentCapacity > minimumCapacity && !editableRuntimeOption(template, "dataVolume", true) {
		return nil, fmt.Errorf("the latest product version fixes shared data volume capacity below the current %.0f GiB; data volumes can only be expanded", currentCapacity)
	}
	if len(targetVolumes) == 0 {
		if selection.DataVolumeGiB != nil || len(selection.VolumeSizes) > 0 {
			return nil, fmt.Errorf("this product version does not declare a data volume")
		}
	} else if editableRuntimeOption(template, "dataVolume", true) {
		if selection.DataVolumeGiB == nil {
			value := math.Max(currentCapacity, minimumCapacity)
			if value > 0 {
				selection.DataVolumeGiB = &value
			}
		} else if currentCapacity > 0 && *selection.DataVolumeGiB < currentCapacity {
			return nil, fmt.Errorf("shared data volume capacity can only be expanded (current %.0f GiB)", currentCapacity)
		}
	}
	if editableRuntimeOption(template, "command", false) && selection.Command == nil {
		command, err := runtimepolicy.RuntimeCommand(current)
		if err != nil {
			return nil, err
		}
		if command != nil {
			selection.Command = command
		}
	}
	editableEnv := []string{}
	if values, ok := template["editableEnvKeys"].([]any); ok {
		for _, raw := range values {
			if key, ok := raw.(string); ok {
				editableEnv = append(editableEnv, key)
			}
		}
	}
	if len(editableEnv) > 0 {
		mergedEnvironment := map[string]string{}
		currentEnvironment, _ := current["env"].(map[string]any)
		templateEnvironment, _ := template["env"].(map[string]any)
		for _, key := range editableEnv {
			if value, ok := currentEnvironment[key].(string); ok {
				mergedEnvironment[key] = value
			} else if value, ok := templateEnvironment[key].(string); ok {
				mergedEnvironment[key] = value
			}
		}
		for key, value := range selection.Environment {
			mergedEnvironment[key] = value
		}
		selection.Environment = mergedEnvironment
	}
	if editableRuntimeOption(template, "dependencies", false) && selection.Dependencies == nil {
		dependencies, err := runtimepolicy.RuntimeDependencies(current)
		if err != nil {
			return nil, err
		}
		if dependencies != nil {
			selection.Dependencies = make([]selectedDependency, 0, len(dependencies))
			for _, dependency := range dependencies {
				selection.Dependencies = append(selection.Dependencies, selectedDependency{Key: dependency.Key, ProductID: dependency.ProductID, ServiceSlug: dependency.ServiceSlug, Required: dependency.Required})
			}
		}
	}
	return selectedRuntimeSpec(template, selection)
}

func missingSnapshotSecrets(snapshot map[string]any) ([]string, error) {
	required, err := runtimepolicy.RuntimeSecretKeys(snapshotRuntime(snapshot))
	if err != nil {
		return nil, err
	}
	versions, _ := snapshot["secret_versions"].(map[string]any)
	missing := []string{}
	for _, key := range required {
		versionID, ok := versions[key].(string)
		if !ok || strings.TrimSpace(versionID) == "" {
			missing = append(missing, key)
		}
	}
	return missing, nil
}

func missingRequiredBackups(ctx context.Context, tx pgx.Tx, appID string, runtimeSpec map[string]any, releaseCreated time.Time) ([]string, error) {
	missing := []string{}
	for _, mount := range runtimepolicy.VolumeMounts(runtimeSpec) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_backups backup
			LEFT JOIN app_backup_deletion_jobs deletion ON deletion.backup_id=backup.id
			WHERE backup.user_app_id=$1 AND backup.volume_key=$2 AND backup.status='succeeded'
			  AND backup.completed_at>=$3 AND coalesce(deletion.status,'') NOT IN ('queued','running','succeeded'))`, appID, mount.Key, releaseCreated).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, mount.Key)
		}
	}
	return missing, nil
}
