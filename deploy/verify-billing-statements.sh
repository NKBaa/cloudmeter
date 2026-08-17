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
db(){ compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n1 | tr -d '\r'; }
db_quiet(){ db "$1" >/dev/null 2>&1 || true; }
new_session(){ local uid="$1" token id; token="$(openssl rand -hex 32)"; id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$uid',digest('$token','sha256'),now()+interval '20 minutes') RETURNING id")"; printf '%s|%s\n' "$id" "$token"; }
wait_db(){ local q="$1" expected="$2" seconds="${3:-90}" deadline value=''; deadline=$((SECONDS+seconds)); while ((SECONDS<deadline)); do value="$(db "$q")"; [[ "$value" == "$expected" ]] && return; sleep 2; done; echo "timed out waiting for $expected; last value was $value" >&2; return 1; }
api_json(){ local method="$1" path="$2" auth="${3:-}" body="${4:-}"; local args=(-fsS -X "$method"); [[ -z "$auth" ]] || args+=(-H "Authorization: Bearer $auth"); [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body"); curl "${args[@]}" "$API$path"; }
status(){ local path="$1" auth="${2:-}"; local args=(-sS -o /dev/null -w '%{http_code}'); [[ -z "$auth" ]] || args+=(-H "Authorization: Bearer $auth"); curl "${args[@]}" "$API$path"; }

admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
IFS='|' read -r admin_session admin_token < <(new_session "$admin_id")
marker="$(openssl rand -hex 6)"; password="Statement-$marker-Password!"; usage_code='verify.statement.units'
owner_id=''; other_id=''; override_id=''
cleanup(){
  if [[ -n "$override_id" ]]; then api_json DELETE "/admin/pricing/overrides/$override_id" "$admin_token" >/dev/null 2>&1 || true; db_quiet "DELETE FROM pricing_overrides WHERE id='$override_id'"; fi
  for id in "$owner_id" "$other_id"; do [[ -z "$id" ]] || db_quiet "UPDATE usage_aggregates SET sealed_at=coalesce(sealed_at,now()),billing_disposition=CASE WHEN billing_disposition='pending' THEN 'waived_legacy' ELSE billing_disposition END WHERE user_id='$id'; UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE user_id='$id'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$id'"; done
  db_quiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session'"
}
trap cleanup EXIT

owner_id="$(api_json POST /admin/users "$admin_token" "$(jq -cn --arg email "statement-owner-$marker@example.invalid" --arg password "$password" '{email:$email,password:$password,displayName:"Statement owner",role:"user"}')" | jq -r .id)"
other_id="$(api_json POST /admin/users "$admin_token" "$(jq -cn --arg email "statement-other-$marker@example.invalid" --arg password "$password" '{email:$email,password:$password,displayName:"Statement isolation",role:"user"}')" | jq -r .id)"
IFS='|' read -r owner_session owner_token < <(new_session "$owner_id"); IFS='|' read -r other_session other_token < <(new_session "$other_id")
item_id="$(api_json GET /admin/pricing "$admin_token" | jq -r --arg code "$usage_code" '.items[]?|select(.code==$code and .unit=="unit")|.id' | head -n1)"
if [[ -z "$item_id" ]]; then item_id="$(api_json POST /admin/pricing/items "$admin_token" "$(jq -cn --arg code "$usage_code" '{code:$code,unit:"unit"}')" | jq -r .id)"; fi
now_iso="$(date -u +'%Y-%m-%dT%H:%M:%S.000000Z')"
price_id="$(api_json GET /admin/pricing "$admin_token" | jq -r --arg code "$usage_code" --arg now "$now_iso" '[.items[]?|select(.code==$code and .unit=="unit")|.versions[]?|select((.unitPriceMicros|tonumber)==1000000 and (.precisionScale|tonumber)==6 and .roundingMode=="half_up" and (.minimumQuantity|tonumber)==0 and (.freeQuantity|tonumber)==0 and .effectiveAt <= $now)] | sort_by(.effectiveAt) | if length == 0 then empty else last.id end')"
if [[ -z "$price_id" ]]; then
  effective="$(date -u -d '1 day ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"
  price_id="$(api_json POST "/admin/pricing/items/$item_id/versions" "$admin_token" "$(jq -cn --arg effective "$effective" '{unitPriceMicros:1000000,precisionScale:6,roundingMode:"half_up",minimumQuantity:"0",freeQuantity:"0",effectiveAt:$effective}')" | jq -r .id)"
fi
override_id="$(api_json PUT /admin/pricing/overrides "$admin_token" "$(jq -cn --arg item "$item_id" --arg price "$price_id" --arg user "$owner_id" '{pricingItemId:$item,pricingVersionId:$price,scope:"user",scopeId:$user}')" | jq -r .id)"
api_json POST "/admin/users/$owner_id/wallet/adjust" "$admin_token" "$(jq -cn --arg ref "statement-seed/$marker" '{amountCents:25,businessRef:$ref,note:"Statement verification"}')" >/dev/null
window_start="$(date -u -d '10 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; window_end="$(date -u -d '9 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"
db "INSERT INTO usage_events(user_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$owner_id','$usage_code',7,'unit','$window_start','$window_end','$price_id','statement/$marker')" >/dev/null
wait_db "SELECT count(*) FROM usage_charges WHERE user_id='$owner_id' AND usage_code='$usage_code' AND window_start='$window_start' AND amount_cents=7" 1
bill_id="$(db "SELECT b.id FROM bills b JOIN bill_items i ON i.bill_id=b.id WHERE b.user_id='$owner_id' AND i.usage_code='$usage_code' ORDER BY b.created_at DESC LIMIT 1")"; [[ -n "$bill_id" ]]
[[ "$(status /billing/bills)" == 401 ]] || { echo 'unauthenticated statement request was not rejected' >&2; exit 1; }
list="$(api_json GET /billing/bills "$owner_token")"; detail="$(api_json GET "/billing/bills/$bill_id" "$owner_token")"
jq -e --arg id "$bill_id" '.bills|any(.id==$id)' <<<"$list" >/dev/null
jq -e '([.items[].amountCents]|add)==.bill.totalCents and .bill.totalCents==7' <<<"$detail" >/dev/null
for path in "/billing/bills/$bill_id" "/billing/bills/$bill_id/export"; do [[ "$(status "$path" "$other_token")" == 404 ]] || { echo 'cross-account statement access was not hidden' >&2; exit 1; }; done
headers="$(mktemp)"; csv="$(mktemp)"; curl -fsS -D "$headers" -o "$csv" -H "Authorization: Bearer $owner_token" "$API/billing/bills/$bill_id/export"
grep -qi '^content-type: text/csv' "$headers"; grep -q '费用项' "$csv"; grep -q "$usage_code" "$csv"; rm -f "$headers" "$csv"
echo "Billing statement authentication, dynamic generation, isolation, totals and CSV verification passed ($(jq '.items|length' <<<"$detail") item(s))"
