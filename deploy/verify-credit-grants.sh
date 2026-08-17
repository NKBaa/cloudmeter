#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
DOCKER_BIN="${DOCKER_BIN:-docker}"
COMPOSE=("$DOCKER_BIN" compose)
[[ -n "${COMPOSE_PROJECT_NAME:-}" ]] && COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME")
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
[[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]] && COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE")
API="http://${PLATFORM_HOST:-127.0.0.1}:${PLATFORM_PORT:-8080}/api"

compose(){ "${COMPOSE[@]}" "$@"; }
db(){ compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'; }
db_quiet(){ db "$1" >/dev/null 2>&1 || true; }
token(){ openssl rand -hex 32; }
wait_db(){ local q="$1" expected="$2" seconds="${3:-90}" value='' deadline; deadline=$((SECONDS+seconds)); while ((SECONDS<deadline)); do value="$(db "$q")"; [[ "$value" == "$expected" ]] && return; sleep 2; done; echo "timed out waiting for $expected; last value was $value" >&2; return 1; }
new_session(){ local uid="$1" t id; t="$(token)"; id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$uid',digest('$t','sha256'),now()+interval '20 minutes') RETURNING id")"; printf '%s|%s\n' "$id" "$t"; }
api_json(){ local method="$1" path="$2" auth="${3:-}" body="${4:-}"; local args=(-fsS -X "$method"); [[ -z "$auth" ]] || args+=(-H "Authorization: Bearer $auth"); [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body"); curl "${args[@]}" "$API$path"; }
status(){ local method="$1" path="$2" auth="${3:-}" body="${4:-}"; local args=(-sS -o /dev/null -w '%{http_code}' -X "$method"); [[ -z "$auth" ]] || args+=(-H "Authorization: Bearer $auth"); [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body"); curl "${args[@]}" "$API$path"; }

admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
IFS='|' read -r admin_session admin_token < <(new_session "$admin_id")
marker="$(openssl rand -hex 6)"; password="Credit-$marker-Password!"; usage_code='verify.credit.units'
user_id=''; user_session=''; override_id=''
cleanup(){
  if [[ -n "$override_id" ]]; then api_json DELETE "/admin/pricing/overrides/$override_id" "$admin_token" >/dev/null 2>&1 || true; db_quiet "DELETE FROM pricing_overrides WHERE id='$override_id'"; fi
  if [[ -n "$user_id" ]]; then db_quiet "UPDATE usage_aggregates SET sealed_at=coalesce(sealed_at,now()),billing_disposition=CASE WHEN billing_disposition='pending' THEN 'waived_legacy' ELSE billing_disposition END WHERE user_id='$user_id'; UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE user_id='$user_id'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$user_id'"; fi
  db_quiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session'"
}
trap cleanup EXIT

body="$(jq -cn --arg email "credit-$marker@example.invalid" --arg password "$password" '{email:$email,password:$password,displayName:"Credit verification",role:"user"}')"
user_id="$(api_json POST /admin/users "$admin_token" "$body" | jq -r .id)"
IFS='|' read -r user_session user_token < <(new_session "$user_id")
item_id="$(api_json GET /admin/pricing "$admin_token" | jq -r --arg code "$usage_code" '.items[]?|select(.code==$code and .unit=="unit")|.id' | head -n1)"
if [[ -z "$item_id" ]]; then item_id="$(api_json POST /admin/pricing/items "$admin_token" "$(jq -cn --arg code "$usage_code" '{code:$code,unit:"unit"}')" | jq -r .id)"; fi
now_iso="$(date -u +'%Y-%m-%dT%H:%M:%S.000000Z')"
price_id="$(api_json GET /admin/pricing "$admin_token" | jq -r --arg code "$usage_code" --arg now "$now_iso" '[.items[]?|select(.code==$code and .unit=="unit")|.versions[]?|select((.unitPriceMicros|tonumber)==1000000 and (.precisionScale|tonumber)==6 and .roundingMode=="half_up" and (.minimumQuantity|tonumber)==0 and (.freeQuantity|tonumber)==0 and .effectiveAt <= $now)] | sort_by(.effectiveAt) | if length == 0 then empty else last.id end')"
if [[ -z "$price_id" ]]; then
  effective="$(date -u -d '1 day ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"
  price_id="$(api_json POST "/admin/pricing/items/$item_id/versions" "$admin_token" "$(jq -cn --arg effective "$effective" '{unitPriceMicros:1000000,precisionScale:6,roundingMode:"half_up",minimumQuantity:"0",freeQuantity:"0",effectiveAt:$effective}')" | jq -r .id)"
fi
override_id="$(api_json PUT /admin/pricing/overrides "$admin_token" "$(jq -cn --arg item "$item_id" --arg price "$price_id" --arg user "$user_id" '{pricingItemId:$item,pricingVersionId:$price,scope:"user",scopeId:$user}')" | jq -r .id)"

expired_ref="credit-expired/$marker"; recovery_ref="credit-recovery/$marker"; mixed_ref="credit-mixed/$marker"
recovery_start="$(date -u -d '14 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; recovery_end="$(date -u -d '13 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"
mixed_start="$(date -u -d '12 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; mixed_end="$(date -u -d '11 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"
db "INSERT INTO credit_grants(user_id,amount_cents,remaining_cents,business_ref,note,expires_at,created_by) VALUES('$user_id',9,9,'$expired_ref','Expired validation fixture',now()-interval '1 minute','$admin_id'); INSERT INTO usage_events(user_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$user_id','$usage_code',7,'unit','$recovery_start','$recovery_end','$price_id','credit-recovery/$marker')" >/dev/null
wait_db "SELECT count(*) FROM usage_billing_attempts WHERE user_id='$user_id' AND usage_code='$usage_code' AND window_start='$recovery_start' AND status='insufficient_funds' AND credit_balance_cents=0" 1
expires="$(date -u -d '2 hours' +'%Y-%m-%dT%H:%M:%S.000000Z')"; grant_body="$(jq -cn --arg ref "$recovery_ref" --arg expires "$expires" '{amountCents:7,businessRef:$ref,note:"Recovery credit",expiresAt:$expires}')"
grant="$(api_json POST "/admin/users/$user_id/credits" "$admin_token" "$grant_body")"; replay="$(api_json POST "/admin/users/$user_id/credits" "$admin_token" "$grant_body")"; [[ "$(jq -r .idempotent <<<"$replay")" == true ]]
conflict="$(jq -cn --arg ref "$recovery_ref" '{amountCents:8,businessRef:$ref,note:"Recovery credit"}')"; [[ "$(status POST "/admin/users/$user_id/credits" "$admin_token" "$conflict")" == 409 ]]
wait_db "SELECT count(*) FROM credit_consumptions c JOIN credit_grants g ON g.id=c.credit_grant_id WHERE g.business_ref='$recovery_ref'" 1
order="$(api_json POST /payments/orders "$user_token" "$(jq -cn --arg key "credit-mixed-topup/$marker" '{amountCents:3,provider:"manual",idempotencyKey:$key}')")"; api_json POST "/admin/payments/orders/$(jq -r .orderId <<<"$order")/mark-paid" "$admin_token" '{}' >/dev/null
api_json POST "/admin/users/$user_id/credits" "$admin_token" "$(jq -cn --arg ref "$mixed_ref" '{amountCents:2,businessRef:$ref,note:"Mixed credit"}')" >/dev/null
db "INSERT INTO usage_events(user_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$user_id','$usage_code',5,'unit','$mixed_start','$mixed_end','$price_id','credit-mixed/$marker')" >/dev/null
wait_db "SELECT count(*) FROM usage_charges WHERE user_id='$user_id' AND usage_code='$usage_code' AND window_start='$mixed_start'" 1
result="$(db "SELECT (SELECT remaining_cents FROM credit_grants WHERE business_ref='$expired_ref')||'|'||(SELECT remaining_cents FROM credit_grants WHERE business_ref='$recovery_ref')||'|'||(SELECT remaining_cents FROM credit_grants WHERE business_ref='$mixed_ref')||'|'||(SELECT balance_cents FROM wallets WHERE user_id='$user_id')||'|'||(SELECT c.amount_cents FROM credit_consumptions c JOIN credit_grants g ON g.id=c.credit_grant_id WHERE g.business_ref='$mixed_ref')||'|'||(SELECT amount_cents FROM usage_charges WHERE user_id='$user_id' AND usage_code='$usage_code' AND window_start='$mixed_start')")"
[[ "$result" == '9|0|0|0|2|5' ]] || { echo "credit settlement mismatch: $result" >&2; exit 1; }
grant_id="$(jq -r .id <<<"$grant")"; api_json GET /billing/credits "$user_token" | jq -e --arg id "$grant_id" '.grants|any(.id==$id)' >/dev/null
echo 'Credit expiration, idempotency, grant-only retry, priority consumption, mixed wallet settlement and owner visibility verification passed'
