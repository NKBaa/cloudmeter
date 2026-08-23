package httpapi

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// deleteProductVersion removes a catalog version. Product versions are
// append-only immutable history, so the row is tombstoned with deleted_at and
// disappears from every catalog, deploy and management listing while the
// immutable release snapshots and running instances stay untouched.
func (s *Server) deleteProductVersion(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.PathValue("productID"))
	versionID := strings.TrimSpace(r.PathValue("versionID"))
	if !validUUID(productID) || !validUUID(versionID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "productID and versionID must be UUIDs")
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
	var released int
	var activeTests int
	if err = tx.QueryRow(r.Context(), `SELECT p.slug,v.deleted_at,
			(SELECT count(*) FROM app_releases r WHERE r.product_version_id=v.id),
			(SELECT count(*) FROM app_product_version_tests t WHERE t.product_version_id=v.id AND t.state NOT IN ('succeeded','failed'))
		FROM app_product_versions v JOIN app_products p ON p.id=v.product_id
		WHERE p.id=$1 AND v.id=$2 FOR UPDATE OF v`, productID, versionID).Scan(&slug, &deletedAt, &released, &activeTests); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "version_not_found", "product version not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if deletedAt != nil {
		_ = tx.Commit(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"id": versionID, "deleted": true, "idempotent": true})
		return
	}
	if activeTests > 0 {
		writeError(w, http.StatusConflict, "product_test_in_progress", "wait for version tests to finish before deleting")
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_product_versions SET deleted_at=now() WHERE id=$1 AND product_id=$2`, versionID, productID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,'product.version.delete','app_product_version',$2,$3,jsonb_build_object('product_id',$4::text,'product_slug',$5::text,'release_references',$6::int))`, p.ID, versionID, requestID(r.Context()), productID, slug, released); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": versionID, "deleted": true, "hardDeleted": released == 0})
}
