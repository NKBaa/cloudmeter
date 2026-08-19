#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
DOCKER_BIN="${DOCKER_BIN:-docker}"
COMPOSE=("$DOCKER_BIN" compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME"); fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
if [[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]]; then COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE"); fi
PORT="${PLATFORM_PORT:-8080}"
BASE_URL="http://127.0.0.1:$PORT/api"
db(){ "${COMPOSE[@]}" exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc "$1" | head -n1; }
ADMIN_ID="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
USER_ID="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$ADMIN_ID" && -n "$USER_ID" ]] || { echo 'active super administrator and ordinary user required' >&2; exit 1; }
ADMIN_TOKEN="$(openssl rand -hex 32)"; USER_TOKEN="$(openssl rand -hex 32)"
ADMIN_SESSION="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$ADMIN_ID',digest('$ADMIN_TOKEN','sha256'),now()+interval '15 minutes') RETURNING id")"
USER_SESSION="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$USER_ID',digest('$USER_TOKEN','sha256'),now()+interval '15 minutes') RETURNING id")"
MERCHANT=verify-merchant; SECRET=verify-secret; KEY="verify-epay-$(date +%s%N)"
SAVED="$(db "SELECT enabled::text || chr(9) || merchant_id || chr(9) || endpoint || chr(9) || secret || chr(9) || payment_type FROM payment_provider_configs WHERE provider='epay'")"
cleanup(){
  IFS=$'\t' read -r saved_enabled saved_merchant saved_endpoint saved_secret saved_type <<<"$SAVED"
  saved_merchant=${saved_merchant//'/''}; saved_endpoint=${saved_endpoint//'/''}; saved_secret=${saved_secret//'/''}; saved_type=${saved_type//'/''}
  [[ "$saved_enabled" == true ]] || saved_enabled=false
  db "UPDATE payment_provider_configs SET enabled=$saved_enabled,merchant_id='$saved_merchant',endpoint='$saved_endpoint',secret='$saved_secret',payment_type=coalesce(nullif('$saved_type',''),'alipay'),updated_at=now() WHERE provider='epay'" >/dev/null 2>&1 || true
  db "UPDATE sessions SET revoked_at=now() WHERE id IN ('$ADMIN_SESSION','$USER_SESSION')" >/dev/null 2>&1 || true
}
trap cleanup EXIT
INITIAL="$(db "SELECT balance_cents FROM wallets WHERE user_id='$USER_ID'")"
SETTINGS_BODY=$(printf '{"enabled":true,"merchantId":"%s","endpoint":"https://pay.example.test/submit","secret":"%s"}' "$MERCHANT" "$SECRET")
curl -fsS -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d "$SETTINGS_BODY" "$BASE_URL/admin/settings/payments/epay" >/dev/null
ORDER_BODY=$(printf '{"amountCents":1,"provider":"epay","idempotencyKey":"%s"}' "$KEY")
CREATED="$(curl -fsS -X POST -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' -d "$ORDER_BODY" "$BASE_URL/payments/orders")"
ORDER_ID="$(printf '%s' "$CREATED" | sed -n 's/.*"orderId":"\([^"\]*\)".*/\1/p')"
[[ -n "$ORDER_ID" && "$CREATED" == *'"checkoutUrl"'* ]] || { echo 'EPay checkout URL was not generated' >&2; exit 1; }
NOTIFY="http://127.0.0.1:$PORT/api/payments/epay/callback"; RETURN="http://127.0.0.1:$PORT/console"; TRADE="verify-trade-$(date +%s%N)"
canonical="money=0.01&name=CloudMeter verification&notify_url=$NOTIFY&out_trade_no=$ORDER_ID&pid=$MERCHANT&return_url=$RETURN&trade_no=$TRADE&trade_status=TRADE_SUCCESS&type=alipay"
SIGN="$(printf '%s' "${canonical}${SECRET}" | openssl dgst -md5 -binary | xxd -p -c 256)"
FORM=(--data-urlencode "pid=$MERCHANT" --data-urlencode 'type=alipay' --data-urlencode "out_trade_no=$ORDER_ID" --data-urlencode "notify_url=$NOTIFY" --data-urlencode "return_url=$RETURN" --data-urlencode 'name=CloudMeter verification' --data-urlencode 'money=0.01' --data-urlencode "trade_no=$TRADE" --data-urlencode 'trade_status=TRADE_SUCCESS' --data-urlencode 'sign_type=MD5')
BAD="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${FORM[@]}" --data-urlencode 'sign=bad' "$BASE_URL/payments/epay/callback")"
[[ "$BAD" == 403 ]] || { echo "invalid EPay signature accepted: $BAD" >&2; exit 1; }
VALID="$(curl -fsS -X POST "${FORM[@]}" --data-urlencode "sign=$SIGN" "$BASE_URL/payments/epay/callback")"
[[ "$VALID" == success ]] || { echo 'valid EPay callback rejected' >&2; exit 1; }
REPLAY="$(curl -fsS -X POST "${FORM[@]}" --data-urlencode "sign=$SIGN" "$BASE_URL/payments/epay/callback")"
[[ "$REPLAY" == success ]] || { echo 'EPay callback replay was not idempotent' >&2; exit 1; }
REFUND_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"reason":"EPay provider refund guard verification"}' "$BASE_URL/admin/payments/orders/$ORDER_ID/refund")"
[[ "$REFUND_STATUS" == 503 ]] || { echo "EPay refund without a provider operation returned $REFUND_STATUS" >&2; exit 1; }
[[ "$(db "SELECT status FROM payment_orders WHERE id='$ORDER_ID'")" == paid ]] || { echo 'EPay refund guard changed the order status' >&2; exit 1; }
[[ "$(db "SELECT balance_cents FROM wallets WHERE user_id='$USER_ID'")" == "$((INITIAL+1))" ]] || { echo 'EPay refund guard changed the wallet' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM refunds WHERE order_id='$ORDER_ID'")" == 0 ]] || { echo 'EPay refund guard created a false refund record' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='payment.refund.rejected' AND resource_type='payment_order' AND resource_id='$ORDER_ID' AND metadata->>'cause'='provider_refund_unconfigured'")" == 1 ]] || { echo 'EPay refund rejection audit is missing' >&2; exit 1; }
echo 'WSL EPay checkout, signature rejection, callback idempotency and provider-refund guard passed'
