package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) listCredits(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var available int64
	if err := s.db.QueryRow(r.Context(), `SELECT coalesce(sum(remaining_cents),0) FROM credit_grants WHERE user_id=$1 AND remaining_cents>0 AND (expires_at IS NULL OR expires_at>now())`, p.ID).Scan(&available); err != nil {
		s.internalError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,amount_cents,remaining_cents,business_ref,note,expires_at,created_at FROM credit_grants WHERE user_id=$1 ORDER BY expires_at ASC NULLS LAST,created_at DESC LIMIT 100`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	grants := []map[string]any{}
	now := time.Now()
	for rows.Next() {
		var id, businessRef, note string
		var amount, remaining int64
		var expiresAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &amount, &remaining, &businessRef, &note, &expiresAt, &createdAt); err != nil {
			s.internalError(w, err)
			return
		}
		active := remaining > 0 && (expiresAt == nil || expiresAt.After(now))
		grants = append(grants, map[string]any{"id": id, "amountCents": amount, "remainingCents": remaining, "businessRef": businessRef, "note": note, "expiresAt": expiresAt, "createdAt": createdAt, "active": active})
	}
	consumptionRows, err := s.db.Query(r.Context(), `SELECT c.id,c.credit_grant_id,c.amount_cents,u.usage_code,u.window_start,c.created_at FROM credit_consumptions c JOIN credit_grants g ON g.id=c.credit_grant_id JOIN usage_charges u ON u.id=c.usage_charge_id WHERE g.user_id=$1 ORDER BY c.created_at DESC LIMIT 100`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer consumptionRows.Close()
	consumptions := []map[string]any{}
	for consumptionRows.Next() {
		var id int64
		var grantID, usageCode string
		var amount int64
		var windowStart, createdAt time.Time
		if err := consumptionRows.Scan(&id, &grantID, &amount, &usageCode, &windowStart, &createdAt); err != nil {
			s.internalError(w, err)
			return
		}
		consumptions = append(consumptions, map[string]any{"id": id, "grantId": grantID, "amountCents": amount, "usageCode": usageCode, "windowStart": windowStart, "createdAt": createdAt})
	}
	writeJSON(w, 200, map[string]any{"availableCents": available, "grants": grants, "consumptions": consumptions})
}

func (s *Server) grantCredit(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	userID := r.PathValue("userID")
	var q struct {
		AmountCents int64      `json:"amountCents"`
		BusinessRef string     `json:"businessRef"`
		Note        string     `json:"note"`
		ExpiresAt   *time.Time `json:"expiresAt"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	q.BusinessRef = strings.TrimSpace(q.BusinessRef)
	q.Note = strings.TrimSpace(q.Note)
	if q.AmountCents <= 0 || q.AmountCents > 1000000000000 || q.BusinessRef == "" || strings.HasPrefix(q.BusinessRef, "subscription-credit/") || len(q.BusinessRef) > 128 || len(q.Note) > 500 {
		writeError(w, 400, "validation_failed", "positive amount and business reference are required")
		return
	}
	if q.ExpiresAt != nil && !q.ExpiresAt.After(time.Now()) {
		writeError(w, 400, "validation_failed", "credit expiration must be in the future")
		return
	}
	if q.ExpiresAt != nil {
		normalized := q.ExpiresAt.UTC().Truncate(time.Microsecond)
		q.ExpiresAt = &normalized
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
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
	var existingID string
	var existingAmount int64
	var existingExpires *time.Time
	var existingNote string
	err = tx.QueryRow(r.Context(), `SELECT id,amount_cents,expires_at,note FROM credit_grants WHERE user_id=$1 AND business_ref=$2`, userID, q.BusinessRef).Scan(&existingID, &existingAmount, &existingExpires, &existingNote)
	if err == nil {
		if existingAmount != q.AmountCents || existingNote != q.Note || !sameOptionalTime(existingExpires, q.ExpiresAt) {
			writeError(w, 409, "idempotency_conflict", "business reference is already used with different credit parameters")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"id": existingID, "amountCents": existingAmount, "idempotent": true})
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var id string
	if err = tx.QueryRow(r.Context(), `INSERT INTO credit_grants(user_id,amount_cents,remaining_cents,business_ref,note,expires_at,created_by) VALUES($1,$2,$2,$3,$4,$5,$6) RETURNING id`, userID, q.AmountCents, q.BusinessRef, q.Note, q.ExpiresAt, p.ID).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'credit.grant','credit_grant',$3,$4,jsonb_build_object('amount_cents',$5::bigint,'business_ref',$6::text,'expires_at',$7::timestamptz))`, p.ID, userID, id, requestID(r.Context()), q.AmountCents, q.BusinessRef, q.ExpiresAt); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "amountCents": q.AmountCents, "remainingCents": q.AmountCents})
}

func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
