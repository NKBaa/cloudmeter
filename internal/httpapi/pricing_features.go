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

var pricingItemPresets = map[string]string{
	"app.runtime.minutes":     "minute",
	"cpu.core_hours":          "core_hour",
	"memory.gib_hours":        "GiB_hour",
	"storage.data.gib_days":   "GiB_day",
	"network.egress_gib":      "GiB",
	"app.deployment":          "deployment",
	"product.authorization":   "authorization",
	"network.public_ingress":  "ingress",
	"backup.operation":        "operation",
	"backup.storage.gib_days": "GiB_day",
}

func (s *Server) getPricingPublic(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT i.code, v.unit_price_micros FROM pricing_items i JOIN LATERAL (SELECT unit_price_micros FROM pricing_versions WHERE pricing_item_id = i.id AND effective_at <= now() ORDER BY effective_at DESC, version DESC LIMIT 1) v ON true WHERE i.active`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()

	pricing := make(map[string]int64)
	for rows.Next() {
		var code string
		var price int64
		if err := rows.Scan(&code, &price); err != nil {
			s.internalError(w, err)
			return
		}
		pricing[code] = price
	}
	writeJSON(w, http.StatusOK, pricing)
}

func (s *Server) getPricingCatalogPublic(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT i.code,i.unit,v.unit_price_micros
		FROM pricing_items i
		JOIN LATERAL (SELECT unit_price_micros FROM pricing_versions WHERE pricing_item_id=i.id AND effective_at<=now() ORDER BY effective_at DESC,version DESC LIMIT 1) v ON true
		WHERE i.active AND i.code <> 'storage.system.gib_days' ORDER BY i.code`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var code, unit string
		var unitPriceMicros int64
		if err := rows.Scan(&code, &unit, &unitPriceMicros); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"code": code, "unit": unit, "unitPriceMicros": unitPriceMicros})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminPricing(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT i.id,i.code,i.unit,i.created_at,v.id,v.version,v.unit_price_micros,v.precision_scale,v.rounding_mode,v.minimum_quantity::text,v.free_quantity::text,v.effective_at,v.created_at FROM pricing_items i LEFT JOIN pricing_versions v ON v.pricing_item_id=i.id WHERE i.active AND i.code <> 'storage.system.gib_days' ORDER BY i.code,v.version DESC`)
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
	presetUnit, ok := pricingItemPresets[q.Code]
	if !ok {
		writeError(w, 400, "validation_failed", "pricing item must use a supported preset")
		return
	}
	q.Unit = presetUnit
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	if err = tx.QueryRow(r.Context(), `INSERT INTO pricing_items(code,unit,active) VALUES($1,$2,true)
		ON CONFLICT(code) DO UPDATE SET active=true WHERE pricing_items.unit=excluded.unit AND NOT pricing_items.active
		RETURNING id`, q.Code, q.Unit).Scan(&id); err != nil {
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

func (s *Server) deletePricingItem(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	itemID := r.PathValue("itemID")
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var code string
	if err = tx.QueryRow(r.Context(), `UPDATE pricing_items SET active=false WHERE id=$1 AND active RETURNING code`, itemID).Scan(&code); err == pgx.ErrNoRows {
		writeError(w, 404, "pricing_item_not_found", "pricing item not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'pricing.item.delete','pricing_item',$2,$3,jsonb_build_object('code',$4::text))`, p.ID, itemID, requestID(r.Context()), code); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	var itemCode string
	if err = tx.QueryRow(r.Context(), "SELECT code FROM pricing_items WHERE id=$1 AND active FOR UPDATE", itemID).Scan(&itemCode); err == pgx.ErrNoRows {
		writeError(w, 404, "pricing_item_not_found", "pricing item not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if itemCode == "storage.system.gib_days" {
		writeError(w, 409, "pricing_item_retired", "system disk billing has been retired")
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
