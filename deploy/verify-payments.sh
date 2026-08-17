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
BASE_URL="http://127.0.0.1:${PORT}/api"
LATEST_MIGRATION="$(find migrations -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -n 1)"
TMP_DIR="$(mktemp -d)"
ADMIN_SESSION=''
USER_SESSION=''
USER_ID=''

db() { "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'; }
assert_db_rejected() {
  local query="$1" description="$2"
  if "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c "$query" >/dev/null 2>&1; then
    echo "database accepted forbidden mutation: $description" >&2
    exit 1
  fi
}
session() {
  local user_id="$1" token id
  token="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')"
  id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$user_id',digest('$token','sha256'),now()+interval '15 minutes') RETURNING id")"
  printf '%s|%s\n' "$id" "$token"
}
cleanup() {
  local ids=() joined=''
  [[ -z "$ADMIN_SESSION" ]] || ids+=("'$ADMIN_SESSION'")
  [[ -z "$USER_SESSION" ]] || ids+=("'$USER_SESSION'")
  if (( ${#ids[@]} )); then
    joined="$(IFS=,; echo "${ids[*]}")"
    db "UPDATE sessions SET revoked_at=now() WHERE id IN ($joined) RETURNING id" >/dev/null 2>&1 || true
  fi
  [[ -z "$USER_ID" ]] || db "UPDATE users SET status='suspended' WHERE id='$USER_ID' RETURNING id" >/dev/null 2>&1 || true
  rm -f "$TMP_DIR"/refund-*
  rmdir "$TMP_DIR" 2>/dev/null || true
}
trap cleanup EXIT

MIGRATION_STATE="$(db "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations")"
[[ "$MIGRATION_STATE" == "$LATEST_MIGRATION|clean" ]] || {
  echo "migration $LATEST_MIGRATION must be applied before payment verification; current state is $MIGRATION_STATE" >&2
  exit 1
}
ADMIN_ID="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$ADMIN_ID" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
IFS='|' read -r ADMIN_SESSION ADMIN_TOKEN <<<"$(session "$ADMIN_ID")"
AUTH_ADMIN=(-H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json')
MARKER="$(date +%s%N)-$RANDOM"
PASSWORD="Pay-${MARKER}-verify"
ACCOUNT_BODY="$(jq -cn --arg email "payment-verify-$MARKER@example.invalid" --arg password "$PASSWORD" '{email:$email,password:$password,displayName:"Payment refund verification",role:"user"}')"
USER_ID="$(curl -fsS -X POST "${AUTH_ADMIN[@]}" -d "$ACCOUNT_BODY" "$BASE_URL/admin/users" | jq -r .id)"
[[ -n "$USER_ID" && "$USER_ID" != null ]] || { echo 'temporary user creation failed' >&2; exit 1; }
IFS='|' read -r USER_SESSION USER_TOKEN <<<"$(session "$USER_ID")"
AUTH_USER=(-H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json')

INITIAL="$(db "SELECT balance_cents FROM wallets WHERE user_id='$USER_ID'")"
KEY="verify-payment-$MARKER"
BODY="$(jq -cn --arg key "$KEY" '{amountCents:1,provider:"manual",idempotencyKey:$key}')"
CREATED="$(curl -fsS -X POST "${AUTH_USER[@]}" -d "$BODY" "$BASE_URL/payments/orders")"
ORDER_ID="$(jq -r .orderId <<<"$CREATED")"
[[ -n "$ORDER_ID" && "$ORDER_ID" != null ]] || { echo 'order creation failed' >&2; exit 1; }
REPLAY="$(curl -fsS -X POST "${AUTH_USER[@]}" -d "$BODY" "$BASE_URL/payments/orders")"
[[ "$(jq -r .idempotent <<<"$REPLAY")" == true && "$(jq -r .orderId <<<"$REPLAY")" == "$ORDER_ID" ]] || { echo 'order replay not idempotent' >&2; exit 1; }
CONFLICT="$(jq -cn --arg key "$KEY" '{amountCents:2,provider:"manual",idempotencyKey:$key}')"
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${AUTH_USER[@]}" -d "$CONFLICT" "$BASE_URL/payments/orders")" == 409 ]] || { echo 'idempotency conflict not rejected' >&2; exit 1; }
PAID="$(curl -fsS -X POST "${AUTH_ADMIN[@]}" -d '{}' "$BASE_URL/admin/payments/orders/$ORDER_ID/mark-paid")"
PAID_REPLAY="$(curl -fsS -X POST "${AUTH_ADMIN[@]}" -d '{}' "$BASE_URL/admin/payments/orders/$ORDER_ID/mark-paid")"
[[ "$(jq -r .balanceCents <<<"$PAID")" == "$((INITIAL+1))" && "$(jq -r .idempotent <<<"$PAID_REPLAY")" == true ]] || {
  echo 'manual payment replay changed the wallet incorrectly' >&2
  exit 1
}

REFUND_BODY="$(jq -cn --arg reason "concurrent refund verification $MARKER" '{reason:$reason}')"
pids=()
for i in 1 2; do
  (curl -sS -o "$TMP_DIR/refund-$i.body" -w '%{http_code}' -X POST "${AUTH_ADMIN[@]}" -d "$REFUND_BODY" "$BASE_URL/admin/payments/orders/$ORDER_ID/refund" >"$TMP_DIR/refund-$i.code") &
  pids+=("$!")
done
for pid in "${pids[@]}"; do wait "$pid"; done
for i in 1 2; do
  [[ "$(cat "$TMP_DIR/refund-$i.code")" == 200 ]] || {
    echo "concurrent refund $i failed: $(cat "$TMP_DIR/refund-$i.body")" >&2
    exit 1
  }
done
REFUND_RESULTS="$(jq -s '.' "$TMP_DIR/refund-1.body" "$TMP_DIR/refund-2.body")"
[[ "$(jq '[.[].refundId] | unique | length' <<<"$REFUND_RESULTS")" == 1 && "$(jq '[.[].ledgerEntryId] | unique | length' <<<"$REFUND_RESULTS")" == 1 ]] || {
  echo 'concurrent refunds returned different immutable records' >&2
  exit 1
}
[[ "$(jq '[.[] | select(.idempotent == false)] | length' <<<"$REFUND_RESULTS")" == 1 && "$(jq '[.[] | select(.idempotent == true)] | length' <<<"$REFUND_RESULTS")" == 1 ]] || {
  echo 'concurrent refunds did not produce exactly one mutation and one replay' >&2
  exit 1
}
REFUND_ID="$(jq -r '.[0].refundId' <<<"$REFUND_RESULTS")"
LEDGER_ID="$(jq -r '.[0].ledgerEntryId' <<<"$REFUND_RESULTS")"
REFUND_REPLAY="$(curl -fsS -X POST "${AUTH_ADMIN[@]}" -d "$REFUND_BODY" "$BASE_URL/admin/payments/orders/$ORDER_ID/refund")"
[[ "$(jq -r .idempotent <<<"$REFUND_REPLAY")" == true && "$(jq -r .refundId <<<"$REFUND_REPLAY")" == "$REFUND_ID" && "$(jq -r .ledgerEntryId <<<"$REFUND_REPLAY")" == "$LEDGER_ID" ]] || {
  echo 'refund replay did not return the original refund record' >&2
  exit 1
}
REFUND_LIST="$(curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$BASE_URL/admin/payments/refunds")"
[[ "$(jq --arg id "$REFUND_ID" '[.refunds[] | select(.id==$id and .status=="succeeded" and (.events|length)==2)] | length' <<<"$REFUND_LIST")" == 1 ]] || {
  echo 'administrator refund timeline query is incomplete' >&2
  exit 1
}

FINAL="$(db "SELECT balance_cents FROM wallets WHERE user_id='$USER_ID'")"
LEDGER_COUNT="$(db "SELECT count(*) FROM wallet_ledger_entries entry JOIN wallets wallet ON wallet.id=entry.wallet_id WHERE wallet.user_id='$USER_ID' AND entry.business_ref='$ORDER_ID' AND entry.business_type IN ('topup','refund')")"
[[ "$FINAL" == "$INITIAL" && "$LEDGER_COUNT" == 2 ]] || { echo 'wallet or append-only ledger invariant failed' >&2; exit 1; }
REFUND_INVARIANT="$(db "SELECT count(*) FROM refunds rf JOIN payment_orders o ON o.id=rf.order_id JOIN wallet_ledger_entries le ON le.id=rf.ledger_entry_id JOIN wallets w ON w.id=le.wallet_id WHERE rf.id='$REFUND_ID' AND rf.status='succeeded' AND o.status='refunded' AND rf.user_id='$USER_ID' AND w.user_id=rf.user_id AND le.business_type='refund' AND le.business_ref=o.id::text AND le.amount_cents=-o.amount_cents AND rf.ledger_entry_id=$LEDGER_ID")"
[[ "$REFUND_INVARIANT" == 1 ]] || { echo 'refund snapshot, order and ledger are not aligned' >&2; exit 1; }
[[ "$(db "SELECT string_agg(to_status,',' ORDER BY id) FROM refund_events WHERE refund_id='$REFUND_ID'")" == 'processing,succeeded' ]] || { echo 'refund event timeline is invalid' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='payment.refund' AND resource_type='refund' AND resource_id='$REFUND_ID' AND subject_user_id='$USER_ID' AND metadata->>'order_id'='$ORDER_ID'")" == 1 ]] || { echo 'refund audit record is missing' >&2; exit 1; }

assert_db_rejected "UPDATE refunds SET reason=reason || ' changed' WHERE id='$REFUND_ID'" 'refund identity update'
assert_db_rejected "DELETE FROM refunds WHERE id='$REFUND_ID'" 'refund deletion'
assert_db_rejected "UPDATE refund_events SET message=message || ' changed' WHERE refund_id='$REFUND_ID'" 'refund event update'
assert_db_rejected "DELETE FROM refund_events WHERE refund_id='$REFUND_ID'" 'refund event deletion'
assert_db_rejected 'BEGIN; TRUNCATE refund_events; ROLLBACK;' 'refund event truncation'
assert_db_rejected 'BEGIN; TRUNCATE refunds CASCADE; ROLLBACK;' 'refund history truncation'
assert_db_rejected "UPDATE wallet_ledger_entries SET amount_cents=amount_cents+1 WHERE id=$LEDGER_ID" 'wallet ledger update'
assert_db_rejected "DELETE FROM wallet_ledger_entries WHERE id=$LEDGER_ID" 'wallet ledger deletion'
assert_db_rejected 'BEGIN; TRUNCATE wallet_ledger_entries CASCADE; ROLLBACK;' 'wallet ledger truncation'
assert_db_rejected "UPDATE payment_orders SET amount_cents=amount_cents+1 WHERE id='$ORDER_ID'" 'refunded order snapshot update'

echo "Payment/refund verification passed; order $ORDER_ID, refund $REFUND_ID, wallet net change 0 cents"
