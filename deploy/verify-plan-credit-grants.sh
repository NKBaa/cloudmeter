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
  session_id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$user_id',digest('$token','sha256'),now()+interval '15 minutes') RETURNING id")"
  printf '%s|%s\n' "$session_id" "$token"
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
IFS='|' read -r aid at < <(session "$admin_id")
uid=''; tmp=''
cleanup() {
  [[ -z "$aid" ]] || db "UPDATE sessions SET revoked_at=now() WHERE id='$aid'" >/dev/null || true
  [[ -z "$uid" ]] || db "UPDATE sessions SET revoked_at=now() WHERE id='$uid'" >/dev/null || true
  [[ -z "$tmp" ]] || rm -rf "$tmp"
}
trap cleanup EXIT

marker="$(openssl rand -hex 6)"
password="PlanCredit-$marker-Password!"
auth=(-H "Authorization: Bearer $at" -H 'Content-Type: application/json')

[[ "$(curl -sS -o /dev/null -w '%{http_code}' "$API/admin/plans")" == 401 ]]
user_id="$(curl -fsS "${auth[@]}" -d "{\"email\":\"plan-credit-$marker@example.invalid\",\"password\":\"$password\",\"displayName\":\"Plan credit verification\",\"role\":\"user\"}" "$API/admin/users" | jq -r .id)"
IFS='|' read -r uid ut < <(session "$user_id")
user_auth=(-H "Authorization: Bearer $ut" -H 'Content-Type: application/json')
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "${user_auth[@]}" -X PUT -d '{"planVersionId":"00000000-0000-0000-0000-000000000000","endsAt":null}' "$API/admin/users/$user_id/subscription")" == 403 ]]
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "${auth[@]}" -d '{"amountCents":1,"businessRef":"subscription-credit/reserved","note":"must fail"}' "$API/admin/users/$user_id/credits")" == 400 ]]

plan_id="$(curl -fsS "${auth[@]}" -d "{\"code\":\"verify-credit-$marker\",\"name\":\"Credit verification $marker\"}" "$API/admin/plans" | jq -r .id)"
body1="$(jq -cn --arg effectiveAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '{cyclePriceCents:0,apps:1,cpuCores:1,memoryGiB:1,dataDiskGiB:0,backupStorageGiB:0,backupOperationsPerMonth:0,concurrentDeployments:1,publicIngresses:1,ingressOverageEnabled:false,egressGiB:1,egressOverageEnabled:false,creditGrantCents:1234,allowedProductIds:[],effectiveAt:$effectiveAt}')"
v1="$(curl -fsS "${auth[@]}" -d "$body1" "$API/admin/plans/$plan_id/versions" | jq -r .id)"
assign1="$(jq -cn --arg id "$v1" '{planVersionId:$id,endsAt:null}')"
curl -fsS "${auth[@]}" -X PUT -d "$assign1" "$API/admin/users/$user_id/subscription" >/dev/null
curl -fsS "${auth[@]}" -X PUT -d "$assign1" "$API/admin/users/$user_id/subscription" >/dev/null
prefix="subscription-credit/$user_id/"
[[ "$(db "SELECT count(*) FROM credit_grants WHERE user_id='$user_id' AND business_ref LIKE '$prefix%'")" == 1 ]]
[[ "$(db "SELECT coalesce(sum(amount_cents),0) FROM credit_grants WHERE user_id='$user_id' AND business_ref LIKE '$prefix%'")" == 1234 ]]

body2="$(jq -c '.creditGrantCents=2345' <<<"$body1")"
v2="$(curl -fsS "${auth[@]}" -d "$body2" "$API/admin/plans/$plan_id/versions" | jq -r .id)"
assign2="$(jq -cn --arg id "$v2" '{planVersionId:$id,endsAt:null}')"
upgrade="$(curl -fsS "${auth[@]}" -X PUT -d "$assign2" "$API/admin/users/$user_id/subscription")"
[[ "$(jq -r .creditGrantedCents <<<"$upgrade")" == 1111 ]]
[[ "$(db "SELECT count(*) FROM credit_grants WHERE user_id='$user_id' AND business_ref LIKE '$prefix%'")" == 2 ]]
[[ "$(db "SELECT coalesce(sum(amount_cents),0) FROM credit_grants WHERE user_id='$user_id' AND business_ref LIKE '$prefix%'")" == 2345 ]]
downgrade="$(curl -fsS "${auth[@]}" -X PUT -d "$assign1" "$API/admin/users/$user_id/subscription")"
[[ "$(jq -r .creditGrantedCents <<<"$downgrade")" == 0 ]]
[[ "$(db "SELECT coalesce(sum(amount_cents),0) FROM credit_grants WHERE user_id='$user_id' AND business_ref LIKE '$prefix%'")" == 2345 ]]
curl -fsS "${user_auth[@]}" "$API/billing/credits" | jq -e '.availableCents==2345' >/dev/null

concurrent_id="$(curl -fsS "${auth[@]}" -d "{\"email\":\"concurrent-credit-$marker@example.invalid\",\"password\":\"$password\",\"displayName\":\"Concurrent credit verification\",\"role\":\"user\"}" "$API/admin/users" | jq -r .id)"
tmp="$(mktemp -d)"
pids=()
for i in {1..6}; do
  (curl -sS -o "$tmp/$i.body" -w '%{http_code}' "${auth[@]}" -X PUT -d "$assign2" "$API/admin/users/$concurrent_id/subscription" >"$tmp/$i.code") &
  pids+=("$!")
done
for pid in "${pids[@]}"; do wait "$pid"; done
for i in {1..6}; do
  [[ "$(cat "$tmp/$i.code")" == 200 ]] || { cat "$tmp/$i.body" >&2; exit 1; }
done
[[ "$(db "SELECT coalesce(sum(amount_cents),0) FROM credit_grants WHERE user_id='$concurrent_id' AND business_ref LIKE 'subscription-credit/$concurrent_id/%'")" == 2345 ]]

worker_id="$(curl -fsS "${auth[@]}" -d "{\"email\":\"worker-credit-$marker@example.invalid\",\"password\":\"$password\",\"displayName\":\"Worker credit verification\",\"role\":\"user\"}" "$API/admin/users" | jq -r .id)"
db "UPDATE user_subscriptions SET plan_version_id='$v2',entitlements_snapshot=(SELECT entitlements FROM plan_versions WHERE id='$v2'),cycle_price_cents_snapshot=0,status='active',starts_at=now(),ends_at=NULL,grace_ends_at=NULL WHERE user_id='$worker_id'" >/dev/null
wait_db "SELECT count(*) FROM credit_grants WHERE user_id='$worker_id' AND business_ref LIKE 'subscription-credit/$worker_id/%'" 1
[[ "$(db "SELECT count(*) FROM audit_logs WHERE subject_user_id='$worker_id' AND action='subscription.credit_grant'")" -ge 1 ]]
[[ "$(db "SELECT bool_and(expires_at=(date_trunc('month',now() AT TIME ZONE 'UTC')+interval '1 month') AT TIME ZONE 'UTC') FROM credit_grants WHERE user_id IN ('$user_id','$concurrent_id','$worker_id') AND business_ref LIKE 'subscription-credit/%'")" == t ]]

echo 'Plan credit entitlement, RBAC, reserved reference, retry and concurrency idempotency, upgrade difference, downgrade retention and Worker reconciliation verification passed'
