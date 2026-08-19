package httpapi

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	appID := strings.TrimSpace(r.PathValue("appID"))
	if !validUUID(appID) {
		writeError(w, 400, "validation_failed", "appID must be a UUID")
		return
	}
	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var slug string
	var deletedAt any
	if err = tx.QueryRow(r.Context(), `SELECT slug,deleted_at FROM user_apps WHERE id=$1 AND user_id=$2 FOR UPDATE`, appID, p.ID).Scan(&slug, &deletedAt); err == pgx.ErrNoRows {
		writeError(w, 404, "app_not_found", "application not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if deletedAt != nil {
		writeJSON(w, 200, map[string]any{"id": appID, "deleted": true, "idempotent": true})
		return
	}
	var active bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE user_app_id=$1 AND state NOT IN ('succeeded','failed')) OR EXISTS(SELECT 1 FROM app_stop_jobs WHERE user_app_id=$1 AND status IN ('queued','running')) OR EXISTS(SELECT 1 FROM app_restore_jobs WHERE user_app_id=$1 AND status IN ('queued','running'))`, appID).Scan(&active); err != nil {
		s.internalError(w, err)
		return
	}
	if active {
		writeError(w, 409, "app_operation_in_progress", "wait for the current application operation to finish before deleting")
		return
	}
	var dependents string
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(string_agg(dependent.slug,', ' ORDER BY dependent.slug),'') FROM user_apps dependent JOIN app_releases release ON release.id=dependent.last_successful_release_id CROSS JOIN LATERAL jsonb_array_elements(coalesce(release.immutable_snapshot->'runtime_spec'->'dependencies','[]'::jsonb)) dependency WHERE dependent.user_id=$2 AND dependent.deleted_at IS NULL AND dependency->>'productId'=(SELECT product_id::text FROM user_apps WHERE id=$1)`, appID, p.ID).Scan(&dependents); err != nil {
		s.internalError(w, err)
		return
	}
	if dependents != "" {
		writeError(w, 409, "app_has_dependents", "applications still depend on this service: "+dependents)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM app_routes WHERE user_app_id=$1`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE user_apps SET deleted_at=now(),status='stopped',suspension_reason=NULL WHERE id=$1`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO app_deletion_jobs(user_app_id) VALUES($1) ON CONFLICT(user_app_id) DO NOTHING`, appID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$1,'app.delete.request','user_app',$2,$3,jsonb_build_object('slug',$4::text))`, p.ID, appID, requestID(r.Context()), slug); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"id": appID, "deleted": true})
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.PathValue("productID"))
	if !validUUID(productID) {
		writeError(w, 400, "validation_failed", "productID must be a UUID")
		return
	}
	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var slug, name string
	var deletedAt any
	if err = tx.QueryRow(r.Context(), `SELECT slug,name,deleted_at FROM app_products WHERE id=$1 FOR UPDATE`, productID).Scan(&slug, &name, &deletedAt); err == pgx.ErrNoRows {
		writeError(w, 404, "product_not_found", "product not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if deletedAt != nil {
		writeJSON(w, 200, map[string]any{"id": productID, "deleted": true, "idempotent": true})
		return
	}
	var appCount, activeTests int
	if err = tx.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM user_apps WHERE product_id=$1),(SELECT count(*) FROM app_product_version_tests t JOIN app_product_versions v ON v.id=t.product_version_id WHERE v.product_id=$1 AND t.state NOT IN ('succeeded','failed'))`, productID).Scan(&appCount, &activeTests); err != nil {
		s.internalError(w, err)
		return
	}
	if appCount > 0 {
		writeError(w, 409, "product_in_use", "product has application history and can only be retired")
		return
	}
	if activeTests > 0 {
		writeError(w, 409, "product_test_in_progress", "wait for version tests to finish before deleting")
		return
	}
	var dependents string
	if err = tx.QueryRow(r.Context(), `SELECT coalesce(string_agg(DISTINCT p.slug,', ' ORDER BY p.slug),'') FROM app_products p JOIN app_product_versions v ON v.product_id=p.id WHERE p.id<>$1 AND p.deleted_at IS NULL AND p.status='published' AND v.published_at IS NOT NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(v.runtime_spec->'dependencies','[]'::jsonb)) d WHERE d->>'productId'=$1::text)`, productID).Scan(&dependents); err != nil {
		s.internalError(w, err)
		return
	}
	if dependents != "" {
		writeError(w, 409, "product_is_dependency", "published products still depend on this product: "+dependents)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_products SET deleted_at=now(),status='retired' WHERE id=$1`, productID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'product.delete','app_product',$2,$3,jsonb_build_object('slug',$4::text,'name',$5::text))`, p.ID, productID, requestID(r.Context()), slug, name); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": productID, "deleted": true})
}
