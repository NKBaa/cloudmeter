package httpapi

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) startProductVersionTest(w http.ResponseWriter, r *http.Request) {
	productID, versionID := r.PathValue("productID"), r.PathValue("versionID")
	if !validUUID(productID) || !validUUID(versionID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "productID and versionID must be UUIDs")
		return
	}
	p, _ := r.Context().Value(principalKey).(principal)
	var req struct {
		Secrets map[string]string `json:"secrets"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.Secrets == nil {
		req.Secrets = map[string]string{}
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	var productSlug, productStatus, imageDigest string
	var runtimeSpec, routeSpec, healthSpec, updateSpec map[string]any
	var publishedAt *time.Time
	err = tx.QueryRow(r.Context(), `SELECT p.slug,p.status,pv.image_digest,pv.runtime_spec,pv.route_spec,pv.health_spec,pv.update_spec,pv.published_at
		FROM app_products p JOIN app_product_versions pv ON pv.product_id=p.id
		WHERE p.id=$1 AND pv.id=$2 FOR UPDATE OF p,pv`, productID, versionID).Scan(&productSlug, &productStatus, &imageDigest, &runtimeSpec, &routeSpec, &healthSpec, &updateSpec, &publishedAt)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "version_not_found", "product version not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if productStatus == "retired" {
		writeError(w, http.StatusConflict, "product_retired", "restore the product before testing a version")
		return
	}
	if publishedAt != nil {
		writeError(w, http.StatusConflict, "version_already_published", "published versions cannot be retested")
		return
	}
	if err = validateInitialSecrets(runtimeSpec, req.Secrets); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	var activeTestID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM app_product_version_tests WHERE product_version_id=$1 AND state NOT IN ('succeeded','failed') ORDER BY created_at DESC LIMIT 1`, versionID).Scan(&activeTestID)
	if err == nil {
		writeError(w, http.StatusConflict, "test_in_progress", "a test deployment is already in progress for this version")
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}

	var testID string
	if err = tx.QueryRow(r.Context(), "SELECT gen_random_uuid()::text").Scan(&testID); err != nil {
		s.internalError(w, err)
		return
	}
	encryptedSecrets := make(map[string]string, len(req.Secrets))
	for key, value := range req.Secrets {
		encrypted, encryptErr := s.secrets.Encrypt("product.version.test."+testID+"."+key, value)
		if encryptErr != nil {
			s.internalError(w, encryptErr)
			return
		}
		encryptedSecrets[key] = encrypted
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO app_product_version_tests(id,product_version_id,requested_by,immutable_snapshot,encrypted_secrets)
		VALUES($1,$2,$3,jsonb_build_object('product_slug',$4::text,'image_digest',$5::text,'runtime_spec',$6::jsonb,'route_spec',$7::jsonb,'health_spec',$8::jsonb,'update_spec',$9::jsonb),$10::jsonb)`,
		testID, versionID, p.ID, productSlug, imageDigest, runtimeSpec, routeSpec, healthSpec, updateSpec, encryptedSecrets); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_products SET status='testing' WHERE id=$1 AND status='draft'`, productID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'product.test.request','app_product_version',$2,$3,jsonb_build_object('product_id',$4::text,'test_id',$5::text,'secret_keys',$6::text[]))`,
		p.ID, versionID, requestID(r.Context()), productID, testID, mapKeys(req.Secrets)); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"testId": testID, "state": "queued"})
}
