#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
COMPOSE=(docker compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then
  COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME")
fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
PORT="${PLATFORM_PORT:-18080}"
API="http://127.0.0.1:$PORT/api"

db() {
  "${COMPOSE[@]}" exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'
}
session() {
  local user_id="$1" token session_id
  token="$(openssl rand -hex 32)"
  session_id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$user_id',digest('$token','sha256'),now()+interval '20 minutes') RETURNING id")"
  printf '%s|%s\n' "$session_id" "$token"
}
status() {
  local method="$1" uri="$2" token="${3:-}" body="${4:-}"
  local args=(-sS -o /dev/null -w '%{http_code}' -X "$method")
  [[ -z "$token" ]] || args+=(-H "Authorization: Bearer $token")
  [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body")
  curl "${args[@]}" "$uri"
}
wait_db() {
  local query="$1" expected="$2" deadline=$((SECONDS+65)) value=''
  while (( SECONDS < deadline )); do
    value="$(db "$query")"
    [[ "$value" == "$expected" ]] && return 0
    sleep 2
  done
  echo "timed out waiting for $expected; last value was $value" >&2
  return 1
}

admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'active super administrator required' >&2; exit 1; }
IFS='|' read -r admin_session_id admin_token < <(session "$admin_id")
user_id=''
user_session_id=''
plan_id=''
lower_plan_id=''
tmp=''
cleanup() {
  [[ -z "$tmp" ]] || rm -rf "$tmp"
  [[ -z "$user_session_id" ]] || db "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$user_session_id'" >/dev/null || true
  db "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session_id'" >/dev/null || true
  [[ -z "$user_id" ]] || db "UPDATE users SET status='suspended',updated_at=now() WHERE id='$user_id'" >/dev/null || true
  if [[ -n "$plan_id" || -n "$lower_plan_id" ]]; then
    db "UPDATE plans SET purchase_enabled=false WHERE id IN (nullif('$plan_id','')::uuid,nullif('$lower_plan_id','')::uuid)" >/dev/null || true
  fi
}
trap cleanup EXIT

marker="$(openssl rand -hex 6)"
password="Subscription-$marker-Password!"
admin_auth=(-H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json')

user_id="$(curl -fsS "${admin_auth[@]}" -d "{\"email\":\"subscription-$marker@example.invalid\",\"password\":\"$password\",\"displayName\":\"Subscription verification\",\"role\":\"user\"}" "$API/admin/users" | jq -r .id)"
IFS='|' read -r user_session_id user_token < <(session "$user_id")
user_auth=(-H "Authorization: Bearer $user_token" -H 'Content-Type: application/json')
[[ "$(status GET "$API/subscriptions/plans")" == 401 ]]
[[ "$(status GET "$API/admin/plans" "$user_token")" == 403 ]]

plan_id="$(curl -fsS "${admin_auth[@]}" -d "{\"code\":\"verify-cycle-$marker\",\"name\":\"Cycle verification $marker\"}" "$API/admin/plans" | jq -r .id)"
lower_plan_id="$(curl -fsS "${admin_auth[@]}" -d "{\"code\":\"verify-lower-$marker\",\"name\":\"Downgrade verification $marker\"}" "$API/admin/plans" | jq -r .id)"
version_body="$(jq -cn --arg effectiveAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '{cyclePriceCents:1000,apps:2,cpuCores:2,memoryGiB:2,dataDiskGiB:10,backupStorageGiB:0,backupOperationsPerMonth:0,concurrentDeployments:1,publicIngresses:1,ingressOverageEnabled:false,egressGiB:1,egressOverageEnabled:false,creditGrantCents:100,allowedProductIds:[],effectiveAt:$effectiveAt}')"
v1="$(curl -fsS "${admin_auth[@]}" -d "$version_body" "$API/admin/plans/$plan_id/versions" | jq -r .id)"
lower_body="$(jq -c '.cyclePriceCents=500 | .creditGrantCents=0' <<<"$version_body")"
lower="$(curl -fsS "${admin_auth[@]}" -d "$lower_body" "$API/admin/plans/$lower_plan_id/versions" | jq -r .id)"

curl -fsS "${admin_auth[@]}" "$API/admin/plans" | jq -e --arg id "$plan_id" '.plans[] | select(.id==$id) | .purchaseEnabled==false' >/dev/null
if curl -fsS "${user_auth[@]}" "$API/subscriptions/plans" | jq -e --arg first "$plan_id" --arg second "$lower_plan_id" '.plans[] | select(.planId==$first or .planId==$second)' >/dev/null; then
  echo 'disabled plans were visible to a user without a subscription' >&2
  exit 1
fi
unavailable_body="$(jq -cn --arg id "$v1" --arg key "$(openssl rand -hex 16)" '{planVersionId:$id,idempotencyKey:$key}')"
[[ "$(status POST "$API/subscriptions/purchases" "$user_token" "$unavailable_body")" == 404 ]]
[[ "$(status PATCH "$API/admin/plans/$plan_id/availability" "$user_token" '{"enabled":true}')" == 403 ]]
curl -fsS "${admin_auth[@]}" -X PATCH -d '{"enabled":true}' "$API/admin/plans/$plan_id/availability" >/dev/null
curl -fsS "${admin_auth[@]}" -X PATCH -d '{"enabled":true}' "$API/admin/plans/$lower_plan_id/availability" >/dev/null

failed_body="$(jq -cn --arg id "$v1" --arg key "$(openssl rand -hex 16)" '{planVersionId:$id,idempotencyKey:$key}')"
[[ "$(status POST "$API/subscriptions/purchases" "$user_token" "$failed_body")" == 409 ]]
[[ "$(db "SELECT count(*) FROM subscription_purchases WHERE user_id='$user_id' AND status='insufficient_funds'")" == 1 ]]
[[ "$(db "SELECT count(*) FROM wallet_ledger_entries e JOIN wallets w ON w.id=e.wallet_id WHERE w.user_id='$user_id' AND e.business_type='subscription'")" == 0 ]]

curl -fsS "${admin_auth[@]}" -d "{\"amountCents\":5000,\"businessRef\":\"subscription-verify/$marker\",\"note\":\"Subscription purchase verification\"}" "$API/admin/users/$user_id/wallet/adjust" >/dev/null
purchase_body="$(jq -cn --arg id "$v1" --arg key "$(openssl rand -hex 16)" '{planVersionId:$id,idempotencyKey:$key}')"
tmp="$(mktemp -d)"
pids=()
for i in {1..6}; do
  (status POST "$API/subscriptions/purchases" "$user_token" "$purchase_body" >"$tmp/$i.code") &
  pids+=("$!")
done
for pid in "${pids[@]}"; do wait "$pid"; done
created=0
for i in {1..6}; do
  code="$(cat "$tmp/$i.code")"
  [[ "$code" == 200 || "$code" == 201 ]] || { echo "concurrent purchase returned $code" >&2; exit 1; }
  [[ "$code" != 201 ]] || created=$((created+1))
done
[[ "$created" == 1 ]]
[[ "$(db "SELECT count(*) FROM subscription_purchases WHERE user_id='$user_id' AND status='succeeded'")" == 1 ]]
[[ "$(db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'")" == 4000 ]]
initial_ends="$(db "SELECT extract(epoch FROM ends_at)::bigint FROM user_subscriptions WHERE user_id='$user_id'")"

v2_body="$(jq -c '.cyclePriceCents=1800 | .creditGrantCents=200' <<<"$version_body")"
v2="$(curl -fsS "${admin_auth[@]}" -d "$v2_body" "$API/admin/plans/$plan_id/versions" | jq -r .id)"
curl -fsS "${user_auth[@]}" "$API/subscriptions/plans" | jq -e --arg id "$v2" '.plans[] | select(.planVersionId==$id) | .purchaseAction=="upgrade" and .payableCents==800' >/dev/null
upgrade_body="$(jq -cn --arg id "$v2" --arg key "$(openssl rand -hex 16)" '{planVersionId:$id,idempotencyKey:$key}')"
upgrade="$(curl -fsS "${user_auth[@]}" -d "$upgrade_body" "$API/subscriptions/purchases")"
[[ "$(jq -r .purchase.amountCents <<<"$upgrade")" == 800 ]]
[[ "$(db "SELECT extract(epoch FROM ends_at)::bigint FROM user_subscriptions WHERE user_id='$user_id'")" == "$initial_ends" ]]

downgrade_body="$(jq -cn --arg id "$lower" --arg key "$(openssl rand -hex 16)" '{planVersionId:$id,idempotencyKey:$key}')"
downgrade="$(curl -fsS "${user_auth[@]}" -d "$downgrade_body" "$API/subscriptions/purchases")"
[[ "$(jq -r .purchase.amountCents <<<"$downgrade")" == 0 ]]
[[ "$(jq -r .purchase.action <<<"$downgrade")" == downgrade ]]
[[ "$(db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'")" == 3200 ]]
curl -fsS "${admin_auth[@]}" -X PATCH -d '{"enabled":false}' "$API/admin/plans/$lower_plan_id/availability" >/dev/null
curl -fsS "${user_auth[@]}" "$API/subscriptions/plans" | jq -e --arg id "$lower_plan_id" '.plans[] | select(.planId==$id) | .purchaseAction=="renewal"' >/dev/null
renew_body="$(jq -cn --arg id "$lower" --arg key "$(openssl rand -hex 16)" '{planVersionId:$id,idempotencyKey:$key}')"
renew="$(curl -fsS "${user_auth[@]}" -d "$renew_body" "$API/subscriptions/purchases")"
[[ "$(jq -r .purchase.amountCents <<<"$renew")" == 500 ]]
[[ "$(jq -r .purchase.action <<<"$renew")" == renewal ]]
[[ "$(db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'")" == 2700 ]]

statement_total="$(db "SELECT coalesce(sum(amount_cents),0) FROM subscription_bill_items i JOIN bills b ON b.id=i.bill_id WHERE b.user_id='$user_id'")"
ledger_total="$(db "SELECT coalesce(-sum(e.amount_cents),0) FROM wallet_ledger_entries e JOIN wallets w ON w.id=e.wallet_id WHERE w.user_id='$user_id' AND e.business_type='subscription'")"
[[ "$statement_total" == 2300 && "$ledger_total" == 2300 ]]
[[ "$(db "SELECT count(*) FROM subscription_bill_items i JOIN bills b ON b.id=i.bill_id WHERE b.user_id='$user_id'")" == 4 ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE subject_user_id='$user_id' AND action='subscription.purchase'")" -ge 4 ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE resource_id IN ('$plan_id','$lower_plan_id') AND action='plan.availability.update'")" -ge 3 ]]

db "UPDATE user_subscriptions SET status='active',ends_at=now()+interval '1 day',grace_ends_at=NULL WHERE user_id='$user_id'" >/dev/null
wait_db "SELECT count(*) FROM user_notifications WHERE user_id='$user_id' AND kind='subscription_expiring'" 1
db "UPDATE user_subscriptions SET ends_at=now()-interval '1 second' WHERE user_id='$user_id'" >/dev/null
wait_db "SELECT status FROM user_subscriptions WHERE user_id='$user_id'" grace_period
db "UPDATE user_subscriptions SET grace_ends_at=now()-interval '1 second' WHERE user_id='$user_id'" >/dev/null
wait_db "SELECT status FROM user_subscriptions WHERE user_id='$user_id'" expired
[[ "$(db "SELECT count(*) FROM user_notifications WHERE user_id='$user_id' AND kind IN ('subscription_grace','subscription_expired')")" == 2 ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE subject_user_id='$user_id' AND action IN ('subscription.grace_period','subscription.expire')")" == 2 ]]
curl -fsS "${admin_auth[@]}" -X PATCH -d '{"enabled":false}' "$API/admin/plans/$plan_id/availability" >/dev/null

echo 'Plan availability, subscription purchase, concurrency idempotency, price difference, no-refund downgrade, disabled-plan renewal, statement, notification, audit and Worker lifecycle verification passed'
