package httpapi

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// deleteProductVersion archives a catalog version. Release snapshots remain
// immutable and executable, so removing a template never removes an instance.
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
	var archivedAt any
	var publishedAt any
	if err = tx.QueryRow(r.Context(), `SELECT p.slug,v.archived_at,v.published_at
        FROM app_product_versions v JOIN app_products p ON p.id=v.product_id
        WHERE p.id=$1 AND v.id=$2 FOR UPDATE`, productID, versionID).Scan(&slug, &archivedAt, &publishedAt); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "version_not_found", "product version not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if archivedAt != nil {
		writeJSON(w, http.StatusOK, map[string]any{"id": versionID, "archived": true, "idempotent": true})
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_product_versions SET archived_at=now() WHERE id=$1 AND product_id=$2`, versionID, productID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
        VALUES($1,'product.version.archive','app_product_version',$2,$3,jsonb_build_object('product_id',$4::text,'product_slug',$5::text,'published',($6 IS NOT NULL)))`, p.ID, versionID, requestID(r.Context()), productID, slug, publishedAt); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": versionID, "archived": true})
}
