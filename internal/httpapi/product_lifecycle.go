package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const maxProductNameCharacters = 120

func normalizeProductDetails(slug, name string) (string, string, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	name = strings.TrimSpace(name)
	if !appSlugPattern.MatchString(slug) {
		return "", "", fmt.Errorf("product slug must contain only lowercase letters, numbers, and hyphens")
	}
	if name == "" || utf8.RuneCountInString(name) > maxProductNameCharacters {
		return "", "", fmt.Errorf("product name must contain 1 to %d characters", maxProductNameCharacters)
	}
	return slug, name, nil
}

func normalizeProductName(name string) (string, error) {
	_, name, err := normalizeProductDetails("product", name)
	return name, err
}

func normalizeProductIconURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(value) > 2048 {
		return "", fmt.Errorf("product icon must be a valid HTTP or HTTPS URL")
	}
	return value, nil
}

func restoredProductStatus(hasPublishedVersion, hasTestHistory bool) string {
	if hasPublishedVersion {
		return "published"
	}
	if hasTestHistory {
		return "testing"
	}
	return "draft"
}

func (s *Server) updateProduct(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.PathValue("productID"))
	if !validUUID(productID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "productID must be a UUID")
		return
	}
	var q struct {
		Name    string `json:"name"`
		IconURL string `json:"iconUrl"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	name, err := normalizeProductName(q.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	iconURL, err := normalizeProductIconURL(q.IconURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var slug, currentName, currentIconURL, status string
	if err = tx.QueryRow(r.Context(), `SELECT slug,name,icon_url,status FROM app_products WHERE id=$1 FOR UPDATE`, productID).Scan(&slug, &currentName, &currentIconURL, &status); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "product_not_found", "product not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if currentName == name && currentIconURL == iconURL {
		writeJSON(w, http.StatusOK, map[string]any{"id": productID, "slug": slug, "name": name, "iconUrl": iconURL, "status": status, "idempotent": true})
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_products SET name=$2,icon_url=$3 WHERE id=$1`, productID, name, iconURL); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,'product.update','app_product',$2,$3,jsonb_build_object('slug',$4::text,'previous_name',$5::text,'name',$6::text))`,
		p.ID, productID, requestID(r.Context()), slug, currentName, name); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": productID, "slug": slug, "name": name, "iconUrl": iconURL, "status": status, "idempotent": false})
}

func (s *Server) updateProductAvailability(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.PathValue("productID"))
	if !validUUID(productID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "productID must be a UUID")
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

	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = lockProductDependencyGraph(r.Context(), tx); err != nil {
		s.internalError(w, err)
		return
	}
	var slug, name, currentStatus string
	if err = tx.QueryRow(r.Context(), `SELECT slug,name,status FROM app_products WHERE id=$1 FOR UPDATE`, productID).Scan(&slug, &name, &currentStatus); err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "product_not_found", "product not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}

	targetStatus := currentStatus
	if *q.Enabled {
		if currentStatus == "retired" {
			var hasPublishedVersion, hasTestHistory bool
			if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM app_product_versions WHERE product_id=$1 AND published_at IS NOT NULL),
				EXISTS(SELECT 1 FROM app_product_versions pv JOIN app_product_version_tests t ON t.product_version_id=pv.id WHERE pv.product_id=$1)`, productID).Scan(&hasPublishedVersion, &hasTestHistory); err != nil {
				s.internalError(w, err)
				return
			}
			targetStatus = restoredProductStatus(hasPublishedVersion, hasTestHistory)
			if hasPublishedVersion {
				rows, queryErr := tx.Query(r.Context(), `SELECT runtime_spec FROM app_product_versions WHERE product_id=$1 AND published_at IS NOT NULL ORDER BY version`, productID)
				if queryErr != nil {
					s.internalError(w, queryErr)
					return
				}
				specs := make([]map[string]any, 0)
				for rows.Next() {
					var spec map[string]any
					if scanErr := rows.Scan(&spec); scanErr != nil {
						rows.Close()
						s.internalError(w, scanErr)
						return
					}
					specs = append(specs, spec)
				}
				rows.Close()
				if rows.Err() != nil {
					s.internalError(w, rows.Err())
					return
				}
				for _, spec := range specs {
					if dependencyErr := validateProductDependencies(r.Context(), tx, productID, spec); dependencyErr != nil {
						writeError(w, http.StatusConflict, "product_dependency_conflict", dependencyErr.Error())
						return
					}
				}
			}
		}
	} else if currentStatus != "retired" {
		var activeTests int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM app_product_version_tests t JOIN app_product_versions pv ON pv.id=t.product_version_id WHERE pv.product_id=$1 AND t.state NOT IN ('succeeded','failed')`, productID).Scan(&activeTests); err != nil {
			s.internalError(w, err)
			return
		}
		if activeTests > 0 {
			writeError(w, http.StatusConflict, "product_test_in_progress", "product cannot be retired while a version test is in progress")
			return
		}
		var dependentProducts string
		if err = tx.QueryRow(r.Context(), `SELECT coalesce(string_agg(DISTINCT p.slug,', ' ORDER BY p.slug),'')
			FROM app_products p JOIN app_product_versions pv ON pv.product_id=p.id
			WHERE p.id<>$1 AND p.status='published' AND pv.published_at IS NOT NULL
			  AND EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(pv.runtime_spec->'dependencies','[]'::jsonb)) dependency WHERE dependency->>'productId'=$1::text)`, productID).Scan(&dependentProducts); err != nil {
			s.internalError(w, err)
			return
		}
		if dependentProducts != "" {
			writeError(w, http.StatusConflict, "product_is_dependency", "published products still depend on this product: "+dependentProducts)
			return
		}
		targetStatus = "retired"
	}

	if targetStatus == currentStatus {
		writeJSON(w, http.StatusOK, map[string]any{"id": productID, "slug": slug, "name": name, "status": currentStatus, "enabled": currentStatus != "retired", "idempotent": true})
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE app_products SET status=$2 WHERE id=$1`, productID, targetStatus); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1::uuid,'product.availability.update','app_product',$2,$3,jsonb_build_object('slug',$4::text,'from',$5::text,'to',$6::text,'enabled',$7::boolean))`,
		p.ID, productID, requestID(r.Context()), slug, currentStatus, targetStatus, *q.Enabled); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": productID, "slug": slug, "name": name, "status": targetStatus, "enabled": targetStatus != "retired", "idempotent": false})
}
