package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type pricingVersionResponse struct {
	ID              string    `json:"id"`
	Version         int       `json:"version"`
	UnitPriceMicros int64     `json:"unitPriceMicros"`
	PrecisionScale  int16     `json:"precisionScale"`
	RoundingMode    string    `json:"roundingMode"`
	MinimumQuantity string    `json:"minimumQuantity"`
	FreeQuantity    string    `json:"freeQuantity"`
	EffectiveAt     time.Time `json:"effectiveAt"`
	CreatedAt       time.Time `json:"createdAt"`
}

func (s *Server) adminPricing(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT i.id,i.code,i.unit,i.created_at,v.id,v.version,v.unit_price_micros,v.precision_scale,v.rounding_mode,v.minimum_quantity::text,v.free_quantity::text,v.effective_at,v.created_at FROM pricing_items i LEFT JOIN pricing_versions v ON v.pricing_item_id=i.id ORDER BY i.code,v.version DESC`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID        string                   `json:"id"`
		Code      string                   `json:"code"`
		Unit      string                   `json:"unit"`
		CreatedAt time.Time                `json:"createdAt"`
		Versions  []pricingVersionResponse `json:"versions"`
	}
	items := []item{}
	indexes := map[string]int{}
	for rows.Next() {
		var id, code, unit string
		var created time.Time
		var versionID *string
		var version *int
		var price *int64
		var scale *int16
		var mode, minimum, free *string
		var effective, versionCreated *time.Time
		if err := rows.Scan(&id, &code, &unit, &created, &versionID, &version, &price, &scale, &mode, &minimum, &free, &effective, &versionCreated); err != nil {
			s.internalError(w, err)
			return
		}
		idx, ok := indexes[id]
		if !ok {
			idx = len(items)
			indexes[id] = idx
			items = append(items, item{ID: id, Code: code, Unit: unit, CreatedAt: created, Versions: []pricingVersionResponse{}})
		}
		if versionID != nil {
			items[idx].Versions = append(items[idx].Versions, pricingVersionResponse{ID: *versionID, Version: *version, UnitPriceMicros: *price, PrecisionScale: *scale, RoundingMode: *mode, MinimumQuantity: *minimum, FreeQuantity: *free, EffectiveAt: *effective, CreatedAt: *versionCreated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createPricingItem(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		Code string `json:"code"`
		Unit string `json:"unit"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.Code = strings.ToLower(strings.TrimSpace(q.Code))
	q.Unit = strings.ToLower(strings.TrimSpace(q.Unit))
	if q.Code == "" || q.Unit == "" {
		writeError(w, 400, "validation_failed", "code and unit are required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	if err = tx.QueryRow(r.Context(), "INSERT INTO pricing_items(code,unit) VALUES($1,$2) RETURNING id", q.Code, q.Unit).Scan(&id); err != nil {
		writeError(w, 409, "pricing_item_exists", "pricing item already exists")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'pricing.item.create','pricing_item',$2,$3,jsonb_build_object('code',$4::text,'unit',$5::text))`, p.ID, id, requestID(r.Context()), q.Code, q.Unit); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "code": q.Code, "unit": q.Unit})
}

func (s *Server) createPricingVersion(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	itemID := r.PathValue("itemID")
	var q struct {
		UnitPriceMicros int64     `json:"unitPriceMicros"`
		PrecisionScale  int16     `json:"precisionScale"`
		RoundingMode    string    `json:"roundingMode"`
		MinimumQuantity string    `json:"minimumQuantity"`
		FreeQuantity    string    `json:"freeQuantity"`
		EffectiveAt     time.Time `json:"effectiveAt"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if q.UnitPriceMicros < 0 || q.PrecisionScale < 0 || q.PrecisionScale > 12 {
		writeError(w, 400, "validation_failed", "price and precision are invalid")
		return
	}
	if q.RoundingMode == "" {
		q.RoundingMode = "half_up"
	}
	if q.RoundingMode != "up" && q.RoundingMode != "down" && q.RoundingMode != "half_up" {
		writeError(w, 400, "validation_failed", "rounding mode is invalid")
		return
	}
	if q.MinimumQuantity == "" {
		q.MinimumQuantity = "0"
	}
	if q.FreeQuantity == "" {
		q.FreeQuantity = "0"
	}
	if strings.HasPrefix(strings.TrimSpace(q.MinimumQuantity), "-") || strings.HasPrefix(strings.TrimSpace(q.FreeQuantity), "-") {
		writeError(w, 400, "validation_failed", "minimum and free quantities cannot be negative")
		return
	}
	if q.EffectiveAt.IsZero() {
		q.EffectiveAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var exists bool
	if err = tx.QueryRow(r.Context(), "SELECT true FROM pricing_items WHERE id=$1 FOR UPDATE", itemID).Scan(&exists); err == pgx.ErrNoRows {
		writeError(w, 404, "pricing_item_not_found", "pricing item not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	var id string
	var version int
	err = tx.QueryRow(r.Context(), `INSERT INTO pricing_versions(pricing_item_id,version,unit_price_micros,precision_scale,rounding_mode,minimum_quantity,free_quantity,effective_at) SELECT $1,coalesce(max(version),0)+1,$2,$3,$4,$5::numeric,$6::numeric,$7 FROM pricing_versions WHERE pricing_item_id=$1 RETURNING id,version`, itemID, q.UnitPriceMicros, q.PrecisionScale, q.RoundingMode, q.MinimumQuantity, q.FreeQuantity, q.EffectiveAt).Scan(&id, &version)
	if err != nil {
		writeError(w, 400, "validation_failed", "quantity values are invalid")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'pricing.version.create','pricing_version',$2,$3,jsonb_build_object('pricing_item_id',$4::text,'version',$5::int,'unit_price_micros',$6::bigint))`, p.ID, id, requestID(r.Context()), itemID, version, q.UnitPriceMicros); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "version": version})
}

func (s *Server) adminPricingOverrides(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT o.id,o.pricing_item_id,o.pricing_version_id,
		CASE WHEN o.user_id IS NOT NULL THEN 'user' WHEN o.product_id IS NOT NULL THEN 'product' ELSE 'plan' END,
		coalesce(o.user_id,o.product_id,o.plan_id)::text,
		coalesce(u.display_name,p.name,pl.name),i.code,pv.version,o.created_at
		FROM pricing_overrides o
		JOIN pricing_items i ON i.id=o.pricing_item_id
		JOIN pricing_versions pv ON pv.id=o.pricing_version_id
		LEFT JOIN users u ON u.id=o.user_id LEFT JOIN app_products p ON p.id=o.product_id LEFT JOIN plans pl ON pl.id=o.plan_id
		ORDER BY i.code,4,6`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, itemID, versionID, scope, scopeID, scopeName, code string
		var version int
		var created time.Time
		if err := rows.Scan(&id, &itemID, &versionID, &scope, &scopeID, &scopeName, &code, &version, &created); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "pricingItemId": itemID, "pricingVersionId": versionID, "scope": scope, "scopeId": scopeID, "scopeName": scopeName, "code": code, "version": version, "createdAt": created})
	}
	writeJSON(w, 200, map[string]any{"overrides": items})
}

func (s *Server) upsertPricingOverride(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		PricingItemID    string `json:"pricingItemId"`
		PricingVersionID string `json:"pricingVersionId"`
		Scope            string `json:"scope"`
		ScopeID          string `json:"scopeId"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.PricingItemID = strings.TrimSpace(q.PricingItemID)
	q.PricingVersionID = strings.TrimSpace(q.PricingVersionID)
	q.Scope = strings.TrimSpace(q.Scope)
	q.ScopeID = strings.TrimSpace(q.ScopeID)
	if q.PricingItemID == "" || q.PricingVersionID == "" || q.ScopeID == "" || (q.Scope != "user" && q.Scope != "product" && q.Scope != "plan") {
		writeError(w, 400, "validation_failed", "item, version and supported scope are required")
		return
	}
	column := map[string]string{"user": "user_id", "product": "product_id", "plan": "plan_id"}[q.Scope]
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var valid bool
	query := `SELECT EXISTS(SELECT 1 FROM pricing_versions WHERE id=$1 AND pricing_item_id=$2),EXISTS(SELECT 1 FROM ` + map[string]string{"user": "users", "product": "app_products", "plan": "plans"}[q.Scope] + ` WHERE id=$3)`
	var scopeValid bool
	if err = tx.QueryRow(r.Context(), query, q.PricingVersionID, q.PricingItemID, q.ScopeID).Scan(&valid, &scopeValid); err != nil {
		s.internalError(w, err)
		return
	}
	if !valid || !scopeValid {
		writeError(w, 404, "pricing_override_reference_not_found", "pricing version or scope target not found")
		return
	}
	var id string
	sql := `INSERT INTO pricing_overrides(pricing_item_id,pricing_version_id,` + column + `) VALUES($1,$2,$3) ON CONFLICT(` + column + `,pricing_item_id) WHERE ` + column + ` IS NOT NULL DO UPDATE SET pricing_version_id=EXCLUDED.pricing_version_id,created_at=now() RETURNING id`
	if err = tx.QueryRow(r.Context(), sql, q.PricingItemID, q.PricingVersionID, q.ScopeID).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'pricing.override.upsert','pricing_override',$2,$3,jsonb_build_object('pricing_item_id',$4::text,'pricing_version_id',$5::text,'scope',$6::text,'scope_id',$7::text))`, p.ID, id, requestID(r.Context()), q.PricingItemID, q.PricingVersionID, q.Scope, q.ScopeID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "scope": q.Scope, "scopeId": q.ScopeID, "pricingVersionId": q.PricingVersionID})
}

func (s *Server) deletePricingOverride(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var itemID, versionID, scope, scopeID string
	err = tx.QueryRow(r.Context(), `DELETE FROM pricing_overrides WHERE id=$1 RETURNING pricing_item_id,pricing_version_id,CASE WHEN user_id IS NOT NULL THEN 'user' WHEN product_id IS NOT NULL THEN 'product' ELSE 'plan' END,coalesce(user_id,product_id,plan_id)`, r.PathValue("overrideID")).Scan(&itemID, &versionID, &scope, &scopeID)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "pricing_override_not_found", "pricing override not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'pricing.override.delete','pricing_override',$2,$3,jsonb_build_object('pricing_item_id',$4::text,'pricing_version_id',$5::text,'scope',$6::text,'scope_id',$7::text))`, p.ID, r.PathValue("overrideID"), requestID(r.Context()), itemID, versionID, scope, scopeID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}
