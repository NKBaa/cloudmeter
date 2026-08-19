package httpapi

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func verifyEPayCallback(form url.Values, merchant, secret string) (providerCallback, error) {
	values := map[string]string{}
	for key := range form {
		values[key] = form.Get(key)
	}
	orderID := strings.TrimSpace(values["out_trade_no"])
	tradeNo := strings.TrimSpace(values["trade_no"])
	amountText := strings.TrimSpace(values["money"])
	status := strings.TrimSpace(values["trade_status"])
	if orderID == "" || tradeNo == "" || amountText == "" || status == "" || values["pid"] != merchant || !strings.EqualFold(values["sign_type"], "MD5") {
		return providerCallback{}, errors.New("EPay callback fields are invalid")
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(values["sign"])), []byte(epaySignature(values, secret))) != 1 {
		return providerCallback{}, errors.New("EPay callback signature is invalid")
	}
	amount, err := parseAmountCents(amountText)
	if err != nil {
		return providerCallback{}, err
	}
	return providerCallback{OrderID: orderID, AmountCents: amount, ProviderRef: tradeNo, Succeeded: status == "SUCCESS" || status == "TRADE_SUCCESS"}, nil
}

func epaySignature(values map[string]string, secret string) string {
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if k != "sign" && k != "sign_type" && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}
	digest := md5.Sum([]byte(strings.Join(parts, "&") + secret))
	return hex.EncodeToString(digest[:])
}

func epayPurchaseEndpoint(endpoint string) (*url.URL, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("invalid EPay endpoint")
	}
	path := strings.TrimRight(base.Path, "/")
	if path == "" {
		base.Path = "/submit.php"
	} else if !strings.HasSuffix(strings.ToLower(path), "/submit.php") {
		base.Path = path + "/submit.php"
	}
	return base, nil
}

func buildEPayCheckoutURL(endpoint string, values map[string]string, secret string) (string, error) {
	base, err := epayPurchaseEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	query := base.Query()
	for key, value := range values {
		query.Set(key, value)
	}
	query.Set("sign_type", "MD5")
	query.Set("sign", epaySignature(values, secret))
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func (s *Server) epayCallback(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	values := map[string]string{}
	for k := range r.Form {
		values[k] = r.Form.Get(k)
	}
	sign, orderID := strings.TrimSpace(values["sign"]), strings.TrimSpace(values["out_trade_no"])
	status, merchant, amountText, tradeNo := strings.TrimSpace(values["trade_status"]), strings.TrimSpace(values["pid"]), strings.TrimSpace(values["money"]), strings.TrimSpace(values["trade_no"])
	if sign == "" || orderID == "" || amountText == "" || status == "" || tradeNo == "" {
		writeError(w, 400, "invalid_callback", "required callback fields are missing")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(values["sign_type"]), "MD5") {
		writeError(w, 400, "unsupported_signature_type", "only MD5 callbacks are supported")
		return
	}
	if status != "TRADE_SUCCESS" && status != "SUCCESS" {
		writeError(w, 409, "payment_not_succeeded", "payment callback is not a successful trade")
		return
	}
	var enabled bool
	var configuredMerchant, secret string
	if err := s.db.QueryRow(r.Context(), "SELECT enabled,merchant_id,secret FROM payment_provider_configs WHERE provider='epay'").Scan(&enabled, &configuredMerchant, &secret); err != nil {
		s.internalError(w, err)
		return
	}
	if !enabled || secret == "" || configuredMerchant == "" || merchant != configuredMerchant {
		writeError(w, 403, "callback_rejected", "payment provider is not configured")
		return
	}
	secret, err := s.secrets.Decrypt("payment.secret.epay", secret)
	if err != nil {
		s.internalError(w, err)
		return
	}
	expected := epaySignature(values, secret)
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(sign)), []byte(expected)) != 1 {
		writeError(w, 403, "invalid_signature", "callback signature is invalid")
		return
	}
	amountCents, err := parseAmountCents(amountText)
	if err != nil || amountCents <= 0 {
		writeError(w, 400, "invalid_amount", "callback amount is invalid")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var userID, orderStatus string
	var orderAmount int64
	if err = tx.QueryRow(r.Context(), "SELECT user_id,status,amount_cents FROM payment_orders WHERE id=$1 AND provider='epay' FOR UPDATE", orderID).Scan(&userID, &orderStatus, &orderAmount); err == pgx.ErrNoRows {
		writeError(w, 404, "order_not_found", "payment order not found")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if orderAmount != amountCents {
		writeError(w, 400, "amount_mismatch", "callback amount does not match order")
		return
	}
	var conflictingOrderID string
	if err = tx.QueryRow(r.Context(), "SELECT id FROM payment_orders WHERE provider='epay' AND provider_ref=$1 AND id<>$2 FOR UPDATE", tradeNo, orderID).Scan(&conflictingOrderID); err == nil {
		writeError(w, 409, "provider_reference_reused", "payment provider reference is already linked to another order")
		return
	} else if err != pgx.ErrNoRows {
		s.internalError(w, err)
		return
	}
	if orderStatus == "paid" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
		return
	}
	if orderStatus != "pending" {
		writeError(w, 409, "order_not_pending", "payment order is not pending")
		return
	}
	var walletID string
	var balance int64
	if err = tx.QueryRow(r.Context(), "SELECT id,balance_cents FROM wallets WHERE user_id=$1 FOR UPDATE", userID).Scan(&walletID, &balance); err != nil {
		s.internalError(w, err)
		return
	}
	newBalance := balance + amountCents
	var entryID int64
	if err = tx.QueryRow(r.Context(), "INSERT INTO wallet_ledger_entries(wallet_id,business_type,business_ref,amount_cents,balance_after_cents,metadata) VALUES($1,'topup',$2,$3,$4,jsonb_build_object('order_id',$2::text,'provider','epay')) RETURNING id", walletID, orderID, amountCents, newBalance).Scan(&entryID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE wallets SET balance_cents=balance_cents+$1,version=version+1 WHERE id=$2", amountCents, walletID); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE payment_orders SET status='paid',paid_at=now(),provider_ref=$2 WHERE id=$1", orderID, tradeNo); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO payment_order_events(order_id,from_status,to_status,message) VALUES($1,'pending','paid','EPay callback verified')", orderID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("success"))
}

func parseAmountCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, strconv.ErrSyntax
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, strconv.ErrSyntax
	}
	if len(parts) == 1 {
		parts = append(parts, "")
	}
	if len(parts[1]) > 2 {
		return 0, strconv.ErrSyntax
	}
	frac := parts[1]
	for len(frac) < 2 {
		frac += "0"
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, strconv.ErrSyntax
	}
	minor, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, strconv.ErrSyntax
	}
	if whole > (int64(^uint64(0)>>1)-minor)/100 {
		return 0, strconv.ErrRange
	}
	return whole*100 + minor, nil
}
