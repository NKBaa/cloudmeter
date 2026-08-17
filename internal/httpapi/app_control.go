package httpapi

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type appControlRequest struct {
	IdempotencyKey string `json:"idempotencyKey"`
}

func decodeAppControlRequest(w http.ResponseWriter, r *http.Request) (appControlRequest, bool) {
	var q appControlRequest
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return q, false
	}
	q.IdempotencyKey = strings.TrimSpace(q.IdempotencyKey)
	if q.IdempotencyKey == "" || len(q.IdempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, "validation_failed", "idempotencyKey is required and must be at most 128 characters")
		return q, false
	}
	return q, true
}

func (s *Server) stopApp(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	q, ok := decodeAppControlRequest(w, r)
	if !ok {
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	var existingID, existingStatus string
	if err = tx.QueryRow(r.Context(), `SELECT job.id,job.status FROM app_stop_jobs job
		JOIN user_apps app ON app.id=job.user_app_id
		WHERE job.user_app_id=$1 AND job.idempotency_key=$2 AND app.user_id=$3`, appID, q.IdempotencyKey, p.ID).Scan(&existingID, &existingStatus); err == nil {
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"appId": appID, "stopJobId": existingID, "status": existingStatus, "idempotent": true})
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}

	var status, suspensionReason, releaseID, container string
	err = tx.QueryRow(r.Context(), `SELECT app.status,coalesce(app.suspension_reason,''),
		coalesce(app.last_successful_release_id::text,''),coalesce(route.upstream_container,'')
		FROM user_apps app LEFT JOIN app_routes route ON route.user_app_id=app.id
		WHERE app.id=$1 AND app.user_id=$2 FOR UPDATE OF app`, appID, p.ID).Scan(&status, &suspensionReason, &releaseID, &container)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if status == "stopped" {
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"appId": appID, "status": "stopped", "idempotent": true})
		return
	}
	if status == "stopping" {
		writeError(w, http.StatusConflict, "app_stop_in_progress", "application already has an active stop task")
		return
	}
	if status == "suspended" {
		writeError(w, http.StatusConflict, "app_suspended", "application is suspended by platform policy: "+suspensionReason)
		return
	}
	if status != "running" {
		writeError(w, http.StatusConflict, "app_not_running", "only a running application can be stopped")
		return
	}

	var activeDeployment, activeRestore bool
	if err = tx.QueryRow(r.Context(), `SELECT
		EXISTS(SELECT 1 FROM deployment_jobs WHERE user_app_id=$1 AND state NOT IN ('succeeded','failed')),
		EXISTS(SELECT 1 FROM app_restore_jobs WHERE user_app_id=$1 AND status IN ('queued','running'))`, appID).Scan(&activeDeployment, &activeRestore); err != nil {
		s.internalError(w, err)
		return
	}
	if activeDeployment || activeRestore {
		writeError(w, http.StatusConflict, "app_operation_in_progress", "application has an active deployment or restore task")
		return
	}

	var dependents string
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(string_agg(DISTINCT dependent.slug, ', '),'')
		FROM user_apps target
		JOIN user_apps dependent ON dependent.user_id=target.user_id AND dependent.id<>target.id
		JOIN LATERAL (
		  SELECT release.immutable_snapshot FROM app_releases release
		  WHERE release.user_app_id=dependent.id AND release.state<>'failed'
		  ORDER BY release.release_number DESC LIMIT 1
		) snapshot ON true
		CROSS JOIN LATERAL jsonb_array_elements(coalesce(snapshot.immutable_snapshot->'runtime_spec'->'dependencies','[]'::jsonb)) dependency
		WHERE target.id=$1 AND target.user_id=$2
		  AND dependent.status IN ('deploying','running','updating')
		  AND dependency->>'productId'=target.product_id::text
		  AND dependency->>'serviceSlug'=target.service_slug
		  AND coalesce((dependency->>'required')::boolean,false)`, appID, p.ID).Scan(&dependents); err != nil {
		s.internalError(w, err)
		return
	}
	if dependents != "" {
		writeError(w, http.StatusConflict, "app_required_by_dependents", "stop dependent applications first: "+dependents)
		return
	}

	var stopJobID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_stop_jobs(user_app_id,release_id,container_name,idempotency_key)
		VALUES($1,nullif($2,'')::uuid,$3,$4) RETURNING id`, appID, releaseID, container, q.IdempotencyKey).Scan(&stopJobID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM app_routes WHERE user_app_id=$1`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE user_apps SET status='stopping',suspension_reason=NULL WHERE id=$1`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'app.stop.request','user_app',$3,$4,jsonb_build_object('stop_job_id',$5::text,'release_id',nullif($6::text,'')))`, p.auditActorID(), p.ID, appID, requestID(r.Context()), stopJobID, releaseID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"appId": appID, "stopJobId": stopJobID, "status": "stopping", "idempotent": false})
}

func (s *Server) startApp(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "appID must be a UUID")
		return
	}
	q, ok := decodeAppControlRequest(w, r)
	if !ok {
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	var existingJob, existingApp, existingOperation string
	if err = tx.QueryRow(r.Context(), `SELECT job.id,job.user_app_id,job.operation FROM deployment_jobs job
		JOIN user_apps app ON app.id=job.user_app_id WHERE job.idempotency_key=$1 AND app.user_id=$2`, q.IdempotencyKey, p.ID).Scan(&existingJob, &existingApp, &existingOperation); err == nil {
		if existingApp != appID || existingOperation != "start" {
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key is already used for another application operation")
			return
		}
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"appId": appID, "jobId": existingJob, "status": "updating", "idempotent": true})
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}

	if err = enforceDeploymentConcurrency(r.Context(), tx, p.ID); err != nil {
		if quota, quotaOK := err.(resourceQuotaError); quotaOK {
			writeError(w, http.StatusConflict, quota.Code, quota.Message)
			return
		}
		s.internalError(w, err)
		return
	}

	var status, suspensionReason, sourceReleaseID, productID string
	err = tx.QueryRow(r.Context(), `SELECT status,coalesce(suspension_reason,''),coalesce(last_successful_release_id::text,''),product_id::text
		FROM user_apps WHERE id=$1 AND user_id=$2 FOR UPDATE`, appID, p.ID).Scan(&status, &suspensionReason, &sourceReleaseID, &productID)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "app_not_found", "application not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if status == "suspended" {
		writeError(w, http.StatusConflict, "app_suspended", "application is suspended by platform policy: "+suspensionReason)
		return
	}
	if status == "stopping" {
		writeError(w, http.StatusConflict, "app_stop_in_progress", "wait for the stop task to complete before starting")
		return
	}
	if status != "stopped" && status != "failed" {
		writeError(w, http.StatusConflict, "app_not_stopped", "only a stopped or failed application can be started")
		return
	}
	if sourceReleaseID == "" {
		writeError(w, http.StatusConflict, "successful_release_required", "application has no successful release to start")
		return
	}

	var allowed bool
	var ingressLimit, activeIngresses int
	var ingressOverage bool
	err = tx.QueryRow(r.Context(), `SELECT
		NOT (subscription.entitlements_snapshot ? 'allowedProductIds')
		  OR jsonb_array_length(subscription.entitlements_snapshot->'allowedProductIds')=0
		  OR subscription.entitlements_snapshot->'allowedProductIds' ? $2::text,
		coalesce((subscription.entitlements_snapshot->>'publicIngresses')::int,coalesce((subscription.entitlements_snapshot->>'apps')::int,0)),
		(SELECT count(*) FROM user_apps candidate WHERE candidate.user_id=$1 AND candidate.id<>$3 AND candidate.status IN ('deploying','running','updating')),
		coalesce((subscription.entitlements_snapshot->>'ingressOverageEnabled')::boolean,false)
		FROM user_subscriptions subscription WHERE subscription.user_id=$1
		  AND (subscription.status='grace_period' OR (subscription.status='active' AND (subscription.ends_at IS NULL OR subscription.ends_at+interval '3 days'>now())))
		FOR UPDATE`, p.ID, productID, appID).Scan(&allowed, &ingressLimit, &activeIngresses, &ingressOverage)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusConflict, "subscription_required", "an active subscription is required")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "product_not_in_plan", "the current plan does not include this product")
		return
	}
	if activeIngresses >= ingressLimit && !ingressOverage {
		writeError(w, http.StatusConflict, "public_ingress_quota_exceeded", "public ingress entitlement has been reached")
		return
	}

	var versionID string
	var snapshot map[string]any
	if err = tx.QueryRow(r.Context(), `SELECT product_version_id::text,immutable_snapshot FROM app_releases
		WHERE id=$1 AND user_app_id=$2`, sourceReleaseID, appID).Scan(&versionID, &snapshot); err != nil {
		s.internalError(w, err)
		return
	}
	if missing, dependencyErr := missingRuntimeDependencies(r.Context(), tx, p.ID, appID, snapshotRuntime(snapshot)); dependencyErr != nil {
		s.internalError(w, dependencyErr)
		return
	} else if len(missing) > 0 {
		writeError(w, http.StatusConflict, "required_dependency_unavailable", "required dependencies are not running: "+strings.Join(dependencyLabels(missing), ", "))
		return
	}
	if missing, secretErr := missingSnapshotSecrets(snapshot); secretErr != nil {
		s.internalError(w, secretErr)
		return
	} else if len(missing) > 0 {
		writeError(w, http.StatusConflict, "required_secret_missing", "required application Secrets are missing: "+strings.Join(missing, ", "))
		return
	}
	if err = enforceRuntimeEntitlements(r.Context(), tx, p.ID, appID, snapshotRuntime(snapshot)); err != nil {
		if quota, quotaOK := err.(resourceQuotaError); quotaOK {
			writeError(w, http.StatusConflict, quota.Code, quota.Message)
			return
		}
		s.internalError(w, err)
		return
	}

	var releaseNumber int
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(max(release_number),0)+1 FROM app_releases WHERE user_app_id=$1`, appID).Scan(&releaseNumber); err != nil {
		s.internalError(w, err)
		return
	}
	var releaseID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO app_releases(user_app_id,product_version_id,release_number,immutable_snapshot,state)
		VALUES($1,$2,$3,$4,'created') RETURNING id`, appID, versionID, releaseNumber, snapshot).Scan(&releaseID); err != nil {
		s.internalError(w, err)
		return
	}
	var jobID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO deployment_jobs(user_app_id,release_id,idempotency_key,operation,source_release_id)
		VALUES($1,$2,$3,'start',$4) RETURNING id`, appID, releaseID, q.IdempotencyKey, sourceReleaseID).Scan(&jobID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE user_apps SET status='updating',suspension_reason=NULL WHERE id=$1`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'app.start','user_app',$3,$4,jsonb_build_object('job_id',$5::text,'release_id',$6::text,'source_release_id',$7::text))`, p.auditActorID(), p.ID, appID, requestID(r.Context()), jobID, releaseID, sourceReleaseID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"appId": appID, "releaseId": releaseID, "jobId": jobID, "status": "updating", "idempotent": false})
}
