package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type paymentMethod struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	MinAmountCents int64  `json:"minAmountCents"`
	Enabled        bool   `json:"enabled"`
}

var paymentTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

func validatedPaymentMethods(methods []paymentMethod) ([]paymentMethod, error) {
	if len(methods) == 0 || len(methods) > 20 {
		return nil, fmt.Errorf("configure between 1 and 20 payment methods")
	}
	seen := map[string]bool{}
	for index := range methods {
		methods[index].Name = strings.TrimSpace(methods[index].Name)
		methods[index].Type = strings.TrimSpace(methods[index].Type)
		if methods[index].Name == "" || utf8.RuneCountInString(methods[index].Name) > 40 || !paymentTypePattern.MatchString(methods[index].Type) {
			return nil, fmt.Errorf("payment method name or type is invalid")
		}
		if seen[methods[index].Type] {
			return nil, fmt.Errorf("payment method type must be unique")
		}
		if methods[index].MinAmountCents < 100 || methods[index].MinAmountCents > 100000000 {
			return nil, fmt.Errorf("payment method minimum amount must be between 1 and 1000000 yuan")
		}
		seen[methods[index].Type] = true
	}
	return methods, nil
}

func validatedAmountOptions(values []float64) ([]float64, error) {
	if len(values) == 0 || len(values) > 20 {
		return nil, fmt.Errorf("configure between 1 and 20 recharge amounts")
	}
	seen := map[int64]bool{}
	for _, value := range values {
		cents := int64(value*100 + 0.5)
		if cents < 100 || cents > 100000000 || seen[cents] {
			return nil, fmt.Errorf("recharge amounts must be unique values between 1 and 1000000 yuan")
		}
		seen[cents] = true
	}
	return values, nil
}

func centsAmountText(amount int64) string {
	return fmt.Sprintf("%d.%02d", amount/100, amount%100)
}

func refundReasonTooLong(reason string) bool {
	return utf8.RuneCountInString(reason) > 500
}

func supportsImmediateRefund(provider string) bool {
	return provider == "manual"
}

func (s *Server) listPaymentProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT provider,enabled,payment_methods,amount_options FROM payment_provider_configs ORDER BY provider")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var provider string
		var enabled bool
		var methods []paymentMethod
		var amounts []float64
		if err := rows.Scan(&provider, &enabled, &methods, &amounts); err != nil {
			s.internalError(w, err)
			return
		}
		if provider == "epay" {
			visible := make([]paymentMethod, 0, len(methods))
			for _, method := range methods {
				if method.Enabled {
					visible = append(visible, method)
				}
			}
			items = append(items, map[string]any{"provider": provider, "enabled": enabled, "paymentMethods": visible, "amountOptions": amounts})
		} else {
			items = append(items, map[string]any{"provider": provider, "enabled": enabled})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": items})
}

func (s *Server) getPaymentSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), "SELECT provider,enabled,merchant_id,endpoint,(secret<>''),payment_type,payment_methods,amount_options FROM payment_provider_configs ORDER BY provider")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var provider, merchant, endpoint, paymentType string
		var enabled, configured bool
		var methods []paymentMethod
		var amounts []float64
		if err := rows.Scan(&provider, &enabled, &merchant, &endpoint, &configured, &paymentType, &methods, &amounts); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, map[string]any{"provider": provider, "enabled": enabled, "merchantId": merchant, "endpoint": endpoint, "secretConfigured": configured, "paymentType": paymentType, "paymentMethods": methods, "amountOptions": amounts})
	}
	writeJSON(w, 200, map[string]any{"providers": items})
}

func (s *Server) updatePaymentSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	provider := r.PathValue("provider")
	if provider != "manual" && provider != "epay" {
		writeError(w, 404, "provider_not_found", "unsupported payment provider")
		return
	}
	var q struct {
		Enabled        bool            `json:"enabled"`
		MerchantID     string          `json:"merchantId"`
		Endpoint       string          `json:"endpoint"`
		Secret         string          `json:"secret"`
		PaymentType    string          `json:"paymentType"`
		PaymentMethods []paymentMethod `json:"paymentMethods"`
		AmountOptions  []float64       `json:"amountOptions"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	enabled, merchant, endpoint, secret, paymentType := q.Enabled, q.MerchantID, q.Endpoint, q.Secret, strings.TrimSpace(q.PaymentType)
	if paymentType == "" {
		paymentType = "alipay"
	}
	if paymentType != "alipay" && paymentType != "wxpay" && paymentType != "qqpay" && paymentType != "bank" {
		writeError(w, 400, "validation_failed", "unsupported EPay payment type")
		return
	}
	methods, methodsErr := validatedPaymentMethods(q.PaymentMethods)
	if methodsErr != nil {
		writeError(w, 400, "validation_failed", methodsErr.Error())
		return
	}
	amounts, amountsErr := validatedAmountOptions(q.AmountOptions)
	if amountsErr != nil {
		writeError(w, 400, "validation_failed", amountsErr.Error())
		return
	}
	methodsJSON, _ := json.Marshal(methods)
	amountsJSON, _ := json.Marshal(amounts)
	merchant, endpoint = strings.TrimSpace(merchant), strings.TrimSpace(endpoint)
	if endpoint != "" {
		parsed, parseErr := url.ParseRequestURI(endpoint)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			writeError(w, 400, "validation_failed", "payment endpoint must be an HTTP or HTTPS URL")
			return
		}
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var existingSecret string
	if err = tx.QueryRow(r.Context(), "SELECT secret FROM payment_provider_configs WHERE provider=$1 FOR UPDATE", provider).Scan(&existingSecret); err != nil {
		s.internalError(w, err)
		return
	}
	if provider == "epay" && enabled && (merchant == "" || endpoint == "" || (secret == "" && existingSecret == "")) {
		writeError(w, 400, "validation_failed", "enabled EPay requires merchant, endpoint and secret")
		return
	}
	encryptedSecret := ""
	if secret != "" {
		encryptedSecret, err = s.secrets.Encrypt("payment.secret."+provider, secret)
		if err != nil {
			s.internalError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), "UPDATE payment_provider_configs SET enabled=$1,merchant_id=$2,endpoint=$3,secret=CASE WHEN $4='' THEN secret ELSE $4 END,payment_type=$5,payment_methods=$6,amount_options=$7,updated_at=now() WHERE provider=$8", enabled, merchant, endpoint, encryptedSecret, paymentType, methodsJSON, amountsJSON, provider); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,'payment.settings.update','payment_provider_config',$2,$3,jsonb_build_object('enabled',$4::boolean,'merchant_id',$5::text,'endpoint',$6::text,'secret_updated',$7::boolean))`, p.ID, provider, requestID(r.Context()), enabled, merchant, endpoint, secret != ""); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true, "provider": provider})
}

func (s *Server) listPaymentOrders(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	rows, err := s.db.Query(r.Context(), `SELECT o.id,o.provider,o.amount_cents,o.status,o.idempotency_key,o.created_at,o.paid_at,rf.id,rf.status,rf.completed_at
		FROM payment_orders o LEFT JOIN refunds rf ON rf.order_id=o.id
		WHERE o.user_id=$1 ORDER BY o.created_at DESC,o.id DESC LIMIT 50`, p.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, provider, status, key string
		var refundID, refundStatus *string
		var amount int64
		var created time.Time
		var paid, refundCompleted *time.Time
		if err := rows.Scan(&id, &provider, &amount, &status, &key, &created, &paid, &refundID, &refundStatus, &refundCompleted); err != nil {
			s.internalError(w, err)
			return
		}
		item := map[string]any{"id": id, "provider": provider, "amountCents": amount, "status": status, "idempotencyKey": key, "createdAt": created, "paidAt": paid}
		if refundID != nil {
			item["refund"] = map[string]any{"id": *refundID, "status": *refundStatus, "completedAt": refundCompleted}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"orders": items})
}

func (s *Server) adminPaymentOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT o.id,u.email,u.display_name,o.amount_cents,o.provider,o.status,o.created_at,o.paid_at,rf.id,rf.status,rf.completed_at
		FROM payment_orders o JOIN users u ON u.id=o.user_id LEFT JOIN refunds rf ON rf.order_id=o.id
		ORDER BY o.created_at DESC,o.id DESC LIMIT 100`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, email, name, provider, status string
		var refundID, refundStatus *string
		var amount int64
		var created time.Time
		var paid, refundCompleted *time.Time
		if err := rows.Scan(&id, &email, &name, &amount, &provider, &status, &created, &paid, &refundID, &refundStatus, &refundCompleted); err != nil {
			s.internalError(w, err)
			return
		}
		item := map[string]any{"id": id, "email": email, "displayName": name, "amountCents": amount, "provider": provider, "status": status, "createdAt": created, "paidAt": paid}
		if refundID != nil {
			item["refund"] = map[string]any{"id": *refundID, "status": *refundStatus, "completedAt": refundCompleted}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"orders": items})
}

func (s *Server) adminRefunds(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT rf.id,rf.order_id,rf.user_id,u.email,u.display_name,rf.provider,rf.amount_cents,rf.status,rf.reason,
		rf.ledger_entry_id,rf.requested_by,coalesce(actor.email,''),rf.request_id,rf.failure_message,rf.created_at,rf.completed_at
		FROM refunds rf JOIN users u ON u.id=rf.user_id LEFT JOIN users actor ON actor.id=rf.requested_by
		ORDER BY rf.created_at DESC,rf.id DESC LIMIT 100`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()

	items := []map[string]any{}
	positions := map[string]int{}
	for rows.Next() {
		var id, orderID, userID, email, displayName, provider, status, reason, actorEmail, requestIDValue, failureMessage string
		var amount int64
		var ledgerEntryID *int64
		var requestedBy *string
		var createdAt time.Time
		var completedAt *time.Time
		if err := rows.Scan(&id, &orderID, &userID, &email, &displayName, &provider, &amount, &status, &reason, &ledgerEntryID, &requestedBy, &actorEmail, &requestIDValue, &failureMessage, &createdAt, &completedAt); err != nil {
			s.internalError(w, err)
			return
		}
		positions[id] = len(items)
		items = append(items, map[string]any{
			"id": id, "orderId": orderID, "userId": userID, "email": email, "displayName": displayName,
			"provider": provider, "amountCents": amount, "status": status, "reason": reason,
			"ledgerEntryId": ledgerEntryID, "requestedBy": requestedBy, "requestedByEmail": actorEmail,
			"requestId": requestIDValue, "failureMessage": failureMessage, "createdAt": createdAt,
			"completedAt": completedAt, "events": []map[string]any{},
		})
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	rows.Close()
	if len(items) == 0 {
		writeJSON(w, 200, map[string]any{"refunds": items})
		return
	}

	eventRows, err := s.db.Query(r.Context(), `SELECT e.refund_id,e.id,e.from_status,e.to_status,e.message,e.metadata,e.created_at
		FROM refund_events e WHERE e.refund_id IN (SELECT id FROM refunds ORDER BY created_at DESC,id DESC LIMIT 100)
		ORDER BY e.created_at,e.id`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var refundID, toStatus, message string
		var id int64
		var fromStatus *string
		var metadata map[string]any
		var createdAt time.Time
		if err := eventRows.Scan(&refundID, &id, &fromStatus, &toStatus, &message, &metadata, &createdAt); err != nil {
			s.internalError(w, err)
			return
		}
		if pos, ok := positions[refundID]; ok {
			events := items[pos]["events"].([]map[string]any)
			items[pos]["events"] = append(events, map[string]any{"id": id, "fromStatus": fromStatus, "toStatus": toStatus, "message": message, "metadata": metadata, "createdAt": createdAt})
		}
	}
	if err := eventRows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"refunds": items})
}

func (s *Server) createPaymentOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	var q struct {
		AmountCents    int64  `json:"amountCents"`
		Provider       string `json:"provider"`
		PaymentType    string `json:"paymentType"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	amount := q.AmountCents
	provider, key := q.Provider, q.IdempotencyKey
	provider = strings.TrimSpace(provider)
	key = strings.TrimSpace(key)
	if provider == "" {
		provider = "manual"
	}
	if amount <= 0 || amount > 100000000 || key == "" {
		writeError(w, 400, "validation_failed", "positive amount and idempotency key are required")
		return
	}
	var enabled bool
	if err := s.db.QueryRow(r.Context(), "SELECT enabled FROM payment_provider_configs WHERE provider=$1", provider).Scan(&enabled); err == pgx.ErrNoRows || !enabled {
		writeError(w, 503, "payment_provider_unavailable", "payment provider is not configured")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	var checkoutURL string
	if provider != "manual" && provider != "epay" {
		writeError(w, 400, "validation_failed", "unsupported payment provider")
		return
	}
	var epayEndpoint, epaySecret, epayMerchant, epayPaymentType string
	if provider == "epay" {
		var configuredMethods []paymentMethod
		if err := s.db.QueryRow(r.Context(), "SELECT endpoint,secret,merchant_id,payment_type,payment_methods FROM payment_provider_configs WHERE provider='epay'").Scan(&epayEndpoint, &epaySecret, &epayMerchant, &epayPaymentType, &configuredMethods); err != nil {
			s.internalError(w, err)
			return
		}
		requestedType := strings.TrimSpace(q.PaymentType)
		if requestedType == "" && len(configuredMethods) > 0 {
			requestedType = configuredMethods[0].Type
		}
		allowed := false
		for _, method := range configuredMethods {
			if method.Enabled && method.Type == requestedType {
				if amount < method.MinAmountCents {
					writeError(w, 400, "minimum_topup", fmt.Sprintf("minimum recharge for %s is %s yuan", method.Name, centsAmountText(method.MinAmountCents)))
					return
				}
				allowed = true
				break
			}
		}
		if !allowed {
			writeError(w, 400, "payment_method_unavailable", "payment method is not enabled")
			return
		}
		epayPaymentType = requestedType
		if epayEndpoint == "" || epaySecret == "" {
			writeError(w, 503, "payment_provider_pending", "EPay is pending configuration")
			return
		}
		decrypted, decryptErr := s.secrets.Decrypt("payment.secret.epay", epaySecret)
		if decryptErr != nil {
			s.internalError(w, decryptErr)
			return
		}
		epaySecret = decrypted
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var existingID, existingProvider, existingStatus, existingPaymentType string
	var existingAmount int64
	err = tx.QueryRow(r.Context(), "SELECT id,provider,payment_type,amount_cents,status FROM payment_orders WHERE user_id=$1 AND idempotency_key=$2 FOR UPDATE", p.ID, key).Scan(&existingID, &existingProvider, &existingPaymentType, &existingAmount, &existingStatus)
	if err == nil {
		if existingProvider != provider || existingPaymentType != epayPaymentType || existingAmount != amount {
			writeError(w, 409, "idempotency_conflict", "idempotency key is already used with different order parameters")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		response := map[string]any{"orderId": existingID, "provider": existingProvider, "status": existingStatus, "amountCents": existingAmount, "idempotent": true}
		if existingProvider == "epay" && epayEndpoint != "" && epaySecret != "" {
			values := map[string]string{
				"pid":          epayMerchant,
				"type":         existingPaymentType,
				"out_trade_no": existingID,
				"notify_url":   s.requestOrigin(r) + "/api/payments/epay/callback",
				"return_url":   s.requestOrigin(r) + "/console/recharge",
				"name":         "CloudMeter 账户充值",
				"money":        centsAmountText(existingAmount),
				"device":       "pc",
			}
			if checkout, buildErr := buildEPayCheckoutURL(epayEndpoint, values, epaySecret); buildErr == nil {
				response["checkoutUrl"] = checkout
			}
		}
		writeJSON(w, 200, response)
		return
	}
	if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	var id string
	if err = tx.QueryRow(r.Context(), "INSERT INTO payment_orders(user_id,provider,payment_type,amount_cents,idempotency_key) VALUES($1,$2,$3,$4,$5) RETURNING id", p.ID, provider, epayPaymentType, amount, key).Scan(&id); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	if provider == "epay" {
		values := map[string]string{
			"pid":          epayMerchant,
			"type":         epayPaymentType,
			"out_trade_no": id,
			"notify_url":   s.requestOrigin(r) + "/api/payments/epay/callback",
			"return_url":   s.requestOrigin(r) + "/console/recharge",
			"name":         "CloudMeter 账户充值",
			"money":        centsAmountText(amount),
			"device":       "pc",
		}
		var parseErr error
		checkoutURL, parseErr = buildEPayCheckoutURL(epayEndpoint, values, epaySecret)
		if parseErr != nil {
			writeError(w, 503, "payment_provider_pending", "EPay endpoint is invalid")
			return
		}
	}
	response := map[string]any{"orderId": id, "provider": provider, "status": "pending", "amountCents": amount, "idempotent": false}
	if checkoutURL != "" {
		response["checkoutUrl"] = checkoutURL
	}
	writeJSON(w, 201, response)
}

func (s *Server) markPaymentPaid(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	orderID := r.PathValue("orderID")
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var userID, status string
	var amount int64
	if err = tx.QueryRow(r.Context(), "SELECT user_id,status,amount_cents FROM payment_orders WHERE id=$1 FOR UPDATE", orderID).Scan(&userID, &status, &amount); err == pgx.ErrNoRows {
		writeError(w, 404, "order_not_found", "payment order not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if status == "paid" {
		var balance int64
		if err = tx.QueryRow(r.Context(), "SELECT balance_cents FROM wallets WHERE user_id=$1", userID).Scan(&balance); err != nil {
			s.internalError(w, err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"orderId": orderID, "status": status, "balanceCents": balance, "idempotent": true})
		return
	}
	if status != "pending" {
		writeError(w, 409, "order_not_pending", "payment order is not pending")
		return
	}
	var walletID string
	var balance int64
	if err = tx.QueryRow(r.Context(), "SELECT id,balance_cents FROM wallets WHERE user_id=$1 FOR UPDATE", userID).Scan(&walletID, &balance); err != nil {
		s.internalError(w, err)
		return
	}
	newBalance := balance + amount
	var entryID int64
	if err = tx.QueryRow(r.Context(), "INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'topup',$2,$3,$4,jsonb_build_object('order_id',$2::text)) RETURNING id", walletID, orderID, amount, newBalance).Scan(&entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE wallets SET balance_cents=balance_cents+$1,version=version+1 WHERE id=$2", amount, walletID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE payment_orders SET status='paid',paid_at=now() WHERE id=$1", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO payment_order_events(order_id,from_status,to_status,message) VALUES($1,'pending','paid','manual payment verified')", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'payment.mark_paid','payment_order',($3::uuid)::text,$4,jsonb_build_object('amount_cents',$5::bigint,'entry_id',$6::bigint))", p.ID, userID, orderID, requestID(r.Context()), amount, entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"orderId": orderID, "status": "paid", "balanceCents": newBalance})
}

func (s *Server) refundPayment(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(principalKey).(principal)
	orderID := r.PathValue("orderID")
	if !validUUID(orderID) {
		writeError(w, http.StatusBadRequest, "validation_failed", "orderID must be a UUID")
		return
	}
	var q struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &q); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	reason := strings.TrimSpace(q.Reason)
	if reason == "" {
		reason = "administrator full refund"
	}
	if refundReasonTooLong(reason) {
		writeError(w, 400, "validation_failed", "refund reason must not exceed 500 characters")
		return
	}

	// The order row serializes concurrent refund replays without making an
	// already completed request fail with a serializable-transaction retry.
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var userID, provider, status, walletID string
	var amount, balance int64
	if err = tx.QueryRow(r.Context(), "SELECT user_id,provider,status,amount_cents FROM payment_orders WHERE id=$1 FOR UPDATE", orderID).Scan(&userID, &provider, &status, &amount); err == pgx.ErrNoRows {
		writeError(w, 404, "order_not_found", "payment order not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if status == "refunded" {
		var refundID, refundStatus string
		var ledgerEntryID *int64
		var completedAt *time.Time
		if err = tx.QueryRow(r.Context(), `SELECT rf.id,rf.status,rf.ledger_entry_id,rf.completed_at,w.balance_cents
			FROM refunds rf JOIN wallets w ON w.user_id=rf.user_id WHERE rf.order_id=$1`, orderID).Scan(&refundID, &refundStatus, &ledgerEntryID, &completedAt, &balance); err != nil {
			s.internalError(w, err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"orderId": orderID, "status": status, "balanceCents": balance, "refundId": refundID, "refundStatus": refundStatus, "ledgerEntryId": ledgerEntryID, "completedAt": completedAt, "idempotent": true})
		return
	}
	if status == "refunding" {
		writeError(w, 409, "refund_in_progress", "payment refund is already in progress")
		return
	}
	if status != "paid" {
		writeError(w, 409, "order_not_refundable", "only paid orders can be refunded")
		return
	}
	if !supportsImmediateRefund(provider) {
		if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
			VALUES($1::uuid,$2::uuid,'payment.refund.rejected','payment_order',($3::uuid)::text,$4,
			jsonb_build_object('provider',$5::text,'reason',$6::text,'cause','provider_refund_unconfigured'))`,
			p.ID, userID, orderID, requestID(r.Context()), provider, reason); err != nil {
			s.internalError(w, err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "payment_provider_refund_unconfigured", "the payment provider refund operation is not configured")
		return
	}
	if err = tx.QueryRow(r.Context(), "SELECT id,balance_cents FROM wallets WHERE user_id=$1 FOR UPDATE", userID).Scan(&walletID, &balance); err != nil {
		s.internalError(w, err)
		return
	}
	if balance < amount {
		if _, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata)
			VALUES($1::uuid,$2::uuid,'payment.refund.rejected','payment_order',($3::uuid)::text,$4,
			jsonb_build_object('provider',$5::text,'reason',$6::text,'cause','insufficient_balance','balance_cents',$7::bigint,'amount_cents',$8::bigint))`,
			p.ID, userID, orderID, requestID(r.Context()), provider, reason, balance, amount); err != nil {
			s.internalError(w, err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		writeError(w, http.StatusConflict, "insufficient_balance", "wallet balance is lower than refund amount")
		return
	}
	newBalance := balance - amount
	if _, err = tx.Exec(r.Context(), "UPDATE payment_orders SET status='refunding' WHERE id=$1", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO payment_order_events(order_id,from_status,to_status,message) VALUES($1,'paid','refunding','refund initiated')", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	var refundID string
	if err = tx.QueryRow(r.Context(), `INSERT INTO refunds(order_id,user_id,provider,amount_cents,status,reason,requested_by,request_id)
		VALUES($1,$2,$3,$4,'processing',$5,$6,$7) RETURNING id`, orderID, userID, provider, amount, reason, p.ID, requestID(r.Context())).Scan(&refundID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO refund_events(refund_id,from_status,to_status,message) VALUES($1,NULL,'processing','refund requested')", refundID); err != nil {
		s.internalError(w, err)
		return
	}
	var entryID int64
	if err = tx.QueryRow(r.Context(), "INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'refund',$2,$3,$4,jsonb_build_object('order_id',$2::text,'refund_id',$5::text)) RETURNING id", walletID, orderID, -amount, newBalance, refundID).Scan(&entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE wallets SET balance_cents=$1,version=version+1 WHERE id=$2", newBalance, walletID); err != nil {
		s.internalError(w, err)
		return
	}
	var completedAt time.Time
	if err = tx.QueryRow(r.Context(), "UPDATE refunds SET status='succeeded',ledger_entry_id=$2,completed_at=now() WHERE id=$1 RETURNING completed_at", refundID, entryID).Scan(&completedAt); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO refund_events(refund_id,from_status,to_status,message,metadata) VALUES($1,'processing','succeeded','refund completed',jsonb_build_object('ledger_entry_id',$2::bigint))", refundID, entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE payment_orders SET status='refunded' WHERE id=$1", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO payment_order_events(order_id,from_status,to_status,message) VALUES($1,'refunding','refunded','refund completed')", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES($1::uuid,$2::uuid,'payment.refund','refund',($3::uuid)::text,$4,jsonb_build_object('order_id',$5::uuid,'amount_cents',$6::bigint,'entry_id',$7::bigint,'provider',$8::text))", p.ID, userID, refundID, requestID(r.Context()), orderID, amount, entryID, provider); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"orderId": orderID, "status": "refunded", "balanceCents": newBalance, "refundId": refundID, "refundStatus": "succeeded", "ledgerEntryId": entryID, "completedAt": completedAt, "idempotent": false})
}
