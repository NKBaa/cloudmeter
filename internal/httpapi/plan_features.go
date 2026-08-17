package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func validUUID(value string) bool {
	var parsed pgtype.UUID
	return parsed.Scan(value) == nil && parsed.Valid
}

func (s *Server) adminPlans(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT p.id,p.code,p.name,p.purchase_enabled,pv.id,pv.version,pv.cycle_price_cents,pv.entitlements,pv.effective_at FROM plans p LEFT JOIN plan_versions pv ON pv.plan_id=p.id ORDER BY p.created_at,pv.version DESC`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type version struct {
		ID              string         `json:"id"`
		Version         int            `json:"version"`
		CyclePriceCents int64          `json:"cyclePriceCents"`
		Entitlements    map[string]any `json:"entitlements"`
		EffectiveAt     time.Time      `json:"effectiveAt"`
	}
	type plan struct {
		ID              string    `json:"id"`
		Code            string    `json:"code"`
		Name            string    `json:"name"`
		PurchaseEnabled bool      `json:"purchaseEnabled"`
		Versions        []version `json:"versions"`
	}
	items := []plan{}
	index := map[string]int{}
	for rows.Next() {
		var id, code, name string
		var purchaseEnabled bool
		var versionID *string
		var number *int
		var price *int64
		var entitlements map[string]any
		var effective *time.Time
		if err := rows.Scan(&id, &code, &name, &purchaseEnabled, &versionID, &number, &price, &entitlements, &effective); err != nil {
			s.internalError(w, err)
			return
		}
		pos, ok := index[id]
		if !ok {
			pos = len(items)
			index[id] = pos
			items = append(items, plan{ID: id, Code: code, Name: name, PurchaseEnabled: purchaseEnabled, Versions: []version{}})
		}
		if versionID != nil {
			items[pos].Versions = append(items[pos].Versions, version{ID: *versionID, Version: *number, CyclePriceCents: *price, Entitlements: entitlements, EffectiveAt: *effective})
		}
	}
	if err = rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"plans": items})
}

func (s *Server) updatePlanAvailability(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	planID := strings.ToLower(strings.TrimSpace(r.PathValue("planID")))
	if !validUUID(planID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "planID must be a UUID")
		return
	}
	var q struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if q.Enabled == nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "enabled is required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var code, name string
	var previous bool
	if err = tx.QueryRow(r.Context(), `SELECT code,name,purchase_enabled FROM plans WHERE id=$1 FOR UPDATE`, planID).Scan(&code, &name, &previous); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "plan_not_found", "plan not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE plans SET purchase_enabled=$2 WHERE id=$1`, planID, *q.Enabled); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,'plan.availability.update','plan',$2::text,$3,jsonb_build_object('from',$4::boolean,'to',$5::boolean,'code',$6::text))`, p.ID, planID, requestID(r.Context()), previous, *q.Enabled, code); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": planID, "code": code, "name": name, "purchaseEnabled": *q.Enabled})
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.Code = strings.ToLower(strings.TrimSpace(q.Code))
	q.Name = strings.TrimSpace(q.Name)
	if q.Code == "" || q.Name == "" || len(q.Code) > 40 || len(q.Name) > 80 {
		writeError(w, 400, "validation_failed", "code and name are required")
		return
	}
	var id string
	if err := s.db.QueryRow(r.Context(), `INSERT INTO plans(code,name) VALUES($1,$2) RETURNING id`, q.Code, q.Name).Scan(&id); err != nil {
		if strings.Contains(err.Error(), "plans_code_key") {
			writeError(w, 409, "plan_exists", "plan code already exists")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "code": q.Code, "name": q.Name})
}

func (s *Server) createPlanVersion(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	planID := r.PathValue("planID")
	var q struct {
		CyclePriceCents          int64     `json:"cyclePriceCents"`
		Apps                     int       `json:"apps"`
		CPUCores                 float64   `json:"cpuCores"`
		MemoryGiB                int       `json:"memoryGiB"`
		SystemDiskGiB            float64   `json:"systemDiskGiB"`
		DataDiskGiB              float64   `json:"dataDiskGiB"`
		BackupStorageGiB         float64   `json:"backupStorageGiB"`
		BackupOperationsPerMonth int       `json:"backupOperationsPerMonth"`
		ConcurrentDeployments    int       `json:"concurrentDeployments"`
		PublicIngresses          int       `json:"publicIngresses"`
		IngressOverageEnabled    bool      `json:"ingressOverageEnabled"`
		EgressGiB                float64   `json:"egressGiB"`
		EgressOverageEnabled     bool      `json:"egressOverageEnabled"`
		CreditGrantCents         int64     `json:"creditGrantCents"`
		AllowedProductIDs        []string  `json:"allowedProductIds"`
		EffectiveAt              time.Time `json:"effectiveAt"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.CyclePriceCents < 0 || q.CyclePriceCents > 1000000000000 || q.CreditGrantCents < 0 || q.CreditGrantCents > 1000000000000 || q.Apps < 1 || q.Apps > 1000 || q.CPUCores <= 0 || q.MemoryGiB < 1 || q.SystemDiskGiB <= 0 || q.DataDiskGiB < 0 || q.BackupStorageGiB < 0 || q.BackupOperationsPerMonth < 0 || q.ConcurrentDeployments < 1 || q.ConcurrentDeployments > 1000 || q.PublicIngresses < 0 || q.PublicIngresses > 1000 || q.EgressGiB < 0 {
		writeError(w, 400, "validation_failed", "valid price and positive entitlements are required")
		return
	}
	seenProducts := map[string]bool{}
	for i, id := range q.AllowedProductIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if !validUUID(id) || seenProducts[id] {
			writeError(w, 400, "validation_failed", "allowed product ids must be unique UUIDs")
			return
		}
		q.AllowedProductIDs[i] = id
		seenProducts[id] = true
	}
	if q.EffectiveAt.IsZero() {
		q.EffectiveAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if len(q.AllowedProductIDs) > 0 {
		var validProducts int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM app_products WHERE id=ANY($1::uuid[])`, q.AllowedProductIDs).Scan(&validProducts); err != nil || validProducts != len(q.AllowedProductIDs) {
			writeError(w, 400, "validation_failed", "one or more allowed products do not exist")
			return
		}
	}
	var number int
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(max(version),0)+1 FROM plan_versions WHERE plan_id=$1`, planID).Scan(&number); err != nil {
		s.internalError(w, err)
		return
	}
	var id string
	if err = tx.QueryRow(r.Context(), `INSERT INTO plan_versions(plan_id,version,cycle_price_cents,entitlements,effective_at) SELECT $1,$2,$3,jsonb_build_object('apps',$4::int,'cpuCores',$5::numeric,'memoryGiB',$6::int,'systemDiskGiB',$7::numeric,'dataDiskGiB',$8::numeric,'backupStorageGiB',$9::numeric,'backupOperationsPerMonth',$10::int,'concurrentDeployments',$11::int,'publicIngresses',$12::int,'ingressOverageEnabled',$13::boolean,'egressGiB',$14::numeric,'egressOverageEnabled',$15::boolean,'creditGrantCents',$16::bigint,'allowedProductIds',$17::text[]),$18 WHERE EXISTS(SELECT 1 FROM plans WHERE id=$1) RETURNING id`, planID, number, q.CyclePriceCents, q.Apps, q.CPUCores, q.MemoryGiB, q.SystemDiskGiB, q.DataDiskGiB, q.BackupStorageGiB, q.BackupOperationsPerMonth, q.ConcurrentDeployments, q.PublicIngresses, q.IngressOverageEnabled, q.EgressGiB, q.EgressOverageEnabled, q.CreditGrantCents, q.AllowedProductIDs, q.EffectiveAt).Scan(&id); err == pgx.ErrNoRows {
		writeError(w, 404, "plan_not_found", "plan not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'plan.version.create','plan_version',$2::text,$3,jsonb_build_object('plan_id',$4::text,'version',$5::int,'cycle_price_cents',$6::bigint))`, p.ID, id, requestID(r.Context()), planID, number, q.CyclePriceCents); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "version": number})
}

func (s *Server) assignSubscription(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	userID := r.PathValue("userID")
	var q struct {
		PlanVersionID string     `json:"planVersionId"`
		EndsAt        *time.Time `json:"endsAt"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(q.PlanVersionID) == "" {
		writeError(w, 400, "validation_failed", "planVersionId is required")
		return
	}
	if q.EndsAt != nil && !q.EndsAt.After(time.Now()) {
		writeError(w, 400, "validation_failed", "endsAt must be in the future")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var lockedUserID string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM users WHERE id=$1 AND status='active' FOR UPDATE`, userID).Scan(&lockedUserID); err == pgx.ErrNoRows {
		writeError(w, 404, "user_not_found", "active user not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	var planExists bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM plan_versions WHERE id=$1)`, q.PlanVersionID).Scan(&planExists); err != nil {
		s.internalError(w, err)
		return
	}
	if !planExists {
		writeError(w, 404, "plan_version_not_found", "plan version not found")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO user_subscriptions(user_id,plan_version_id,entitlements_snapshot,cycle_price_cents_snapshot,status,ends_at) SELECT $1,pv.id,pv.entitlements,pv.cycle_price_cents,'active',$3 FROM plan_versions pv WHERE pv.id=$2 ON CONFLICT(user_id) DO UPDATE SET plan_version_id=EXCLUDED.plan_version_id,entitlements_snapshot=EXCLUDED.entitlements_snapshot,cycle_price_cents_snapshot=EXCLUDED.cycle_price_cents_snapshot,status='active',starts_at=now(),ends_at=EXCLUDED.ends_at,grace_ends_at=NULL,updated_at=now()`, userID, q.PlanVersionID, q.EndsAt); err != nil {
		s.internalError(w, err)
		return
	}
	var creditGrantID, creditBusinessRef string
	var creditGrantAmount int64
	grantErr := tx.QueryRow(r.Context(), `SELECT id,amount_cents,business_ref
		FROM grant_subscription_credit($1::uuid,$2::uuid)`, userID, p.ID).Scan(&creditGrantID, &creditGrantAmount, &creditBusinessRef)
	if grantErr != nil && grantErr != pgx.ErrNoRows {
		s.internalError(w, grantErr)
		return
	}
	if grantErr == nil {
		if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
			VALUES($1::uuid,$2::uuid,'subscription.credit_grant','credit_grant',$3::text,$4,
			jsonb_build_object('amount_cents',$5::bigint,'business_ref',$6::text,'source','subscription_assignment'))`, p.ID, userID, creditGrantID, requestID(r.Context()), creditGrantAmount, creditBusinessRef); err != nil {
			s.internalError(w, err)
			return
		}
	}
	resumeResult, err := tx.Exec(r.Context(), `INSERT INTO deployment_jobs(user_app_id,release_id,idempotency_key,operation,source_release_id)
		SELECT a.id,a.last_successful_release_id,'subscription-resume/' || a.id::text || '/' || gen_random_uuid()::text,'subscription_recovery',a.last_successful_release_id
		FROM user_apps a
		WHERE a.user_id=$1 AND a.status='suspended' AND a.suspension_reason='subscription_expired'
		  AND a.last_successful_release_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM deployment_jobs j WHERE j.user_app_id=a.id AND j.state NOT IN ('succeeded','failed'))`, userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE user_apps a SET status='updating',suspension_reason=NULL
		WHERE a.user_id=$1 AND a.status='suspended' AND a.suspension_reason='subscription_expired'
		  AND EXISTS (SELECT 1 FROM deployment_jobs j WHERE j.user_app_id=a.id AND j.state='queued' AND j.idempotency_key LIKE 'subscription-resume/%')`, userID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'subscription.assign','user_subscription',($2::uuid)::text,$3,jsonb_build_object('plan_version_id',$4::text,'ends_at',$5::timestamptz))`, p.ID, userID, requestID(r.Context()), q.PlanVersionID, q.EndsAt); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"userId": userID, "planVersionId": q.PlanVersionID, "status": "active", "endsAt": q.EndsAt, "resumeJobs": resumeResult.RowsAffected(), "creditGrantedCents": creditGrantAmount})
}
