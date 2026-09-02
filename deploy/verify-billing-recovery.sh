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
IMAGE='nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10'

compose(){ "${COMPOSE[@]}" "$@"; }
docker(){ MSYS_NO_PATHCONV=1 command "$DOCKER_BIN" "$@"; }
db(){ compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n1 | tr -d '\r'; }
db_quiet(){ db "$1" >/dev/null 2>&1 || true; }
new_session(){ local uid="$1" token id; token="$(openssl rand -hex 32)"; id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$uid',digest('$token','sha256'),now()+interval '30 minutes') RETURNING id")"; printf '%s|%s\n' "$id" "$token"; }
wait_db(){ local q="$1" expected="$2" seconds="${3:-150}" deadline value=''; deadline=$((SECONDS+seconds)); while ((SECONDS<deadline)); do value="$(db "$q")"; [[ "$value" == "$expected" ]] && return; sleep 2; done; echo "timed out waiting for $expected; last value was $value" >&2; return 1; }
wait_deployment(){ local id="$1" deadline=$((SECONDS+${2:-180})) state='' error=''; while ((SECONDS<deadline)); do state="$(db "SELECT state FROM deployment_jobs WHERE id='$id'")"; [[ "$state" == succeeded ]] && return; if [[ "$state" == failed ]]; then error="$(db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$id'")"; echo "deployment failed: $error" >&2; return 1; fi; sleep 1; done; echo "timed out waiting for deployment $id; last state was $state" >&2; return 1; }
wait_product_test(){ local id="$1" deadline=$((SECONDS+${2:-180})) state='' error=''; while ((SECONDS<deadline)); do state="$(db "SELECT state FROM app_product_version_tests WHERE id='$id'")"; [[ "$state" == succeeded ]] && return; if [[ "$state" == failed ]]; then error="$(db "SELECT coalesce(last_error,'') FROM app_product_version_tests WHERE id='$id'")"; echo "product test failed: $error" >&2; return 1; fi; sleep 1; done; echo "timed out waiting for product test $id; last state was $state" >&2; return 1; }
api_json(){ local method="$1" path="$2" auth="${3:-}" body="${4:-}"; local args=(-fsS -X "$method"); [[ -z "$auth" ]] || args+=(-H "Authorization: Bearer $auth"); [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body"); curl "${args[@]}" "$API$path"; }
runtime_scope(){ printf '%s' "$1" | sha256sum | cut -c1-10; }
user_network(){ local owner="$1" user="$2"; if [[ "${owner,,}" == cloudmeter ]]; then printf 'user_net_%s' "$user"; else printf 'user_net_%s-%s' "$(runtime_scope "$owner")" "$user"; fi; }
remove_network(){ local name="$1" owner="$2" labels network_owner member; [[ -n "$name" ]] || return 0; labels="$(docker network inspect --format '{{json .Labels}}' "$name" 2>/dev/null || true)"; [[ -n "$labels" ]] || return 0; network_owner="$(jq -r '.["cloudmeter.owner"] // empty' <<<"$labels")"; [[ "$network_owner" == "$owner" ]] || return 0; while IFS= read -r member; do [[ -z "$member" ]] || docker network disconnect -f "$name" "$member" >/dev/null 2>&1 || true; done < <(docker network inspect --format '{{range .Containers}}{{println .Name}}{{end}}' "$name" 2>/dev/null || true); docker network rm "$name" >/dev/null 2>&1 || true; }

worker_id="$(compose ps -q worker)"; [[ -n "$worker_id" ]] || { echo 'worker must be running' >&2; exit 1; }
mapfile -t worker_env < <(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$worker_id")
printf '%s\n' "${worker_env[@]}" | grep -qx 'DOCKER_EXECUTOR_ENABLED=true' || { echo 'billing recovery verification requires the Docker executor' >&2; exit 1; }
runtime_owner="$(printf '%s\n' "${worker_env[@]}" | sed -n 's/^RUNTIME_OWNER=//p' | head -n1)"; [[ -n "$runtime_owner" ]] || { echo 'worker must expose RUNTIME_OWNER' >&2; exit 1; }
docker image inspect "$IMAGE" >/dev/null
admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"; [[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
IFS='|' read -r admin_session admin_token < <(new_session "$admin_id")
marker="$(openssl rand -hex 6)"; password="Billing-$marker-Password!"; usage_code='verify.recovery.units'; unpriced_code="verify.unpriced.$marker"
user_id=''; product_id=''; plan_id=''; app_id=''; override_id=''; network=''
cleanup(){
  if [[ -n "$override_id" ]]; then api_json DELETE "/admin/pricing/overrides/$override_id" "$admin_token" >/dev/null 2>&1 || true; db_quiet "DELETE FROM pricing_overrides WHERE id='$override_id'"; fi
  if [[ -n "$app_id" ]]; then db_quiet "UPDATE deployment_jobs SET state='failed',last_error=coalesce(last_error,'billing verification cleanup'),updated_at=now() WHERE user_app_id='$app_id' AND state NOT IN ('succeeded','failed'); UPDATE usage_aggregates SET sealed_at=coalesce(sealed_at,now()),billing_disposition=CASE WHEN billing_disposition='pending' THEN 'waived_legacy' ELSE billing_disposition END WHERE user_id='$user_id'; DELETE FROM app_routes WHERE user_app_id='$app_id'; UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE id='$app_id'"; while IFS= read -r c; do [[ -z "$c" ]] || docker rm -f "$c" >/dev/null 2>&1 || true; done < <(docker ps -a --filter "label=cloudmeter.owner=$runtime_owner" --filter "label=cloudmeter.app_id=$app_id" --format '{{.Names}}'); fi
  if [[ -n "$user_id" ]]; then db_quiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE user_id='$user_id'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$user_id'"; fi
  db_quiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session'"
  [[ -z "$plan_id" ]] || db_quiet "UPDATE plans SET purchase_enabled=false WHERE id='$plan_id'"
  [[ -z "$product_id" ]] || db_quiet "UPDATE app_products SET status='retired' WHERE id='$product_id'"
  sleep 3; remove_network "$network" "$runtime_owner"
}
trap cleanup EXIT

product="$(api_json POST /admin/products "$admin_token" "$(jq -cn --arg slug "verify-billing-$marker" --arg name "Billing recovery $marker" '{slug:$slug,name:$name}')")"; product_id="$(jq -r .id <<<"$product")"
version_body="$(jq -cn --arg image "$IMAGE" '{imageDigest:$image,runtimeSpec:{cpuCores:0.25,memoryMiB:128,systemDiskGiB:1,env:{},secretKeys:[],dependencies:[],volumes:[]},routeSpec:{containerPort:80,basePath:"/",websocket:true,sse:true},healthSpec:{path:"/",intervalSeconds:2,timeoutSeconds:3},updateSpec:{dataPolicy:"stateless"}}')"
version="$(api_json POST "/admin/products/$product_id/versions" "$admin_token" "$version_body")"; version_id="$(jq -r .id <<<"$version")"
test="$(api_json POST "/admin/products/$product_id/versions/$version_id/tests" "$admin_token" '{"secrets":{}}')"; wait_product_test "$(jq -r .testId <<<"$test")"
api_json POST "/admin/products/$product_id/versions/$version_id/publish" "$admin_token" | jq -e '.published==true' >/dev/null
plan="$(api_json POST /admin/plans "$admin_token" "$(jq -cn --arg code "verify-billing-$marker" --arg name "Billing recovery $marker" '{code:$code,name:$name}')")"; plan_id="$(jq -r .id <<<"$plan")"
plan_version="$(api_json POST "/admin/plans/$plan_id/versions" "$admin_token" "$(jq -cn --arg product "$product_id" --arg effective "$(date -u +'%Y-%m-%dT%H:%M:%S.000000Z')" '{cyclePriceCents:0,apps:1,cpuCores:1,memoryGiB:1,dataDiskGiB:0,backupStorageGiB:0,backupOperationsPerMonth:0,concurrentDeployments:1,publicIngresses:1,ingressOverageEnabled:false,egressGiB:1,egressOverageEnabled:false,creditGrantCents:0,allowedProductIds:[$product],effectiveAt:$effective}')")"
user_id="$(api_json POST /admin/users "$admin_token" "$(jq -cn --arg email "billing-$marker@example.invalid" --arg password "$password" '{email:$email,password:$password,displayName:"Billing recovery verification",role:"user"}')" | jq -r .id)"; network="$(user_network "$runtime_owner" "$user_id")"
api_json PUT "/admin/users/$user_id/subscription" "$admin_token" "$(jq -cn --arg version "$(jq -r .id <<<"$plan_version")" '{planVersionId:$version,endsAt:null}')" >/dev/null
IFS='|' read -r user_session user_token < <(new_session "$user_id")
item_id="$(api_json GET /admin/pricing "$admin_token" | jq -r --arg code "$usage_code" '.items[]?|select(.code==$code and .unit=="unit")|.id' | head -n1)"; if [[ -z "$item_id" ]]; then item_id="$(api_json POST /admin/pricing/items "$admin_token" "$(jq -cn --arg code "$usage_code" '{code:$code,unit:"unit"}')" | jq -r .id)"; fi
now_iso="$(date -u +'%Y-%m-%dT%H:%M:%S.000000Z')"
price_id="$(api_json GET /admin/pricing "$admin_token" | jq -r --arg code "$usage_code" --arg now "$now_iso" '[.items[]?|select(.code==$code and .unit=="unit")|.versions[]?|select((.unitPriceMicros|tonumber)==1000000 and (.precisionScale|tonumber)==6 and .roundingMode=="half_up" and (.minimumQuantity|tonumber)==0 and (.freeQuantity|tonumber)==0 and .effectiveAt <= $now)] | sort_by(.effectiveAt) | if length == 0 then empty else last.id end')"
if [[ -z "$price_id" ]]; then
  price_id="$(api_json POST "/admin/pricing/items/$item_id/versions" "$admin_token" "$(jq -cn --arg effective "$(date -u -d '1 day ago' +'%Y-%m-%dT%H:%M:%S.000000Z')" '{unitPriceMicros:1000000,precisionScale:6,roundingMode:"half_up",minimumQuantity:"0",freeQuantity:"0",effectiveAt:$effective}')" | jq -r .id)"
fi
override_id="$(api_json PUT /admin/pricing/overrides "$admin_token" "$(jq -cn --arg item "$item_id" --arg price "$price_id" --arg user "$user_id" '{pricingItemId:$item,pricingVersionId:$price,scope:"user",scopeId:$user}')" | jq -r .id)"
deployment="$(api_json POST /apps "$user_token" "$(jq -cn --arg product "$product_id" --arg version "$version_id" --arg key "billing-deploy/$marker" '{productId:$product,versionId:$version,slug:"billing",idempotencyKey:$key,secrets:{}}')")"; app_id="$(jq -r .appId <<<"$deployment")"; wait_deployment "$(jq -r .jobId <<<"$deployment")"; wait_db "SELECT status FROM user_apps WHERE id='$app_id'" running; wait_db "SELECT count(*) FROM app_routes WHERE user_app_id='$app_id'" 1
container="$(db "SELECT upstream_container FROM app_routes WHERE user_app_id='$app_id'")"; docker inspect "$container" >/dev/null
seed="$(api_json POST /payments/orders "$user_token" "$(jq -cn --arg key "billing-seed/$marker" '{amountCents:100,provider:"manual",idempotencyKey:$key}')")"; api_json POST "/admin/payments/orders/$(jq -r .orderId <<<"$seed")/mark-paid" "$admin_token" '{}' >/dev/null; wait_db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'" 100
db "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='waived_legacy' WHERE user_id='$user_id' AND billing_disposition='pending'" >/dev/null
low_start="$(date -u -d '22 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; low_end="$(date -u -d '21 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; db "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$user_id','$app_id','$usage_code',1,'unit','$low_start','$low_end','$price_id','billing-low/$marker')" >/dev/null; wait_db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'" 99; wait_db "SELECT count(*) FROM user_notifications n JOIN usage_aggregates a ON n.event_key='low-balance/'||a.id::text WHERE n.user_id='$user_id' AND a.usage_code='$usage_code' AND a.window_start='$low_start'" 1
db "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='waived_legacy' WHERE user_id='$user_id' AND billing_disposition='pending'" >/dev/null
window_start="$(date -u -d '20 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; window_end="$(date -u -d '19 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; db "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$user_id','$app_id','$usage_code',100,'unit','$window_start','$window_end','$price_id','billing-suspend/$marker')" >/dev/null; wait_db "SELECT status||'|'||coalesce(suspension_reason,'') FROM user_apps WHERE id='$app_id'" 'suspended|billing_insufficient'; wait_db "SELECT count(*) FROM app_routes WHERE user_app_id='$app_id'" 0
db "UPDATE usage_aggregates SET sealed_at=now(),billing_disposition='waived_legacy' WHERE user_id='$user_id' AND billing_disposition='pending' AND NOT (usage_code='$usage_code' AND window_start='$window_start' AND window_end='$window_end')" >/dev/null
unpriced_start="$(date -u -d '18 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"; unpriced_end="$(date -u -d '17 minutes ago' +'%Y-%m-%dT%H:%M:%S.000000Z')"
db "INSERT INTO usage_events(user_id,user_app_id,usage_code,quantity,unit,window_start,window_end,price_version_id,idempotency_key) VALUES('$user_id','$app_id','$unpriced_code',9,'unit','$unpriced_start','$unpriced_end',NULL,'billing-unpriced/$marker')" >/dev/null
wait_db "SELECT count(*) FROM usage_aggregates WHERE user_id='$user_id' AND user_app_id='$app_id' AND usage_code='$unpriced_code' AND window_start='$unpriced_start' AND billing_disposition='unpriced' AND sealed_at IS NOT NULL" 1
[[ "$(db "SELECT count(*) FROM usage_charges WHERE user_id='$user_id' AND user_app_id='$app_id' AND usage_code='$unpriced_code'")" == 0 ]] || { echo 'unpriced usage was charged' >&2; exit 1; }
wait_db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'" 99
topup="$(db "SELECT greatest(0,amount_cents-balance_cents) FROM usage_billing_attempts WHERE user_id='$user_id' AND user_app_id='$app_id' AND usage_code='$usage_code' AND window_start='$window_start' ORDER BY created_at DESC LIMIT 1")"; [[ "$topup" == 1 ]] || { echo "billing attempt required $topup cents instead of 1" >&2; exit 1; }
order="$(api_json POST /payments/orders "$user_token" "$(jq -cn --arg key "billing-topup/$marker" '{amountCents:1,provider:"manual",idempotencyKey:$key}')")"; api_json POST "/admin/payments/orders/$(jq -r .orderId <<<"$order")/mark-paid" "$admin_token" '{}' >/dev/null
wait_db "SELECT balance_cents FROM wallets WHERE user_id='$user_id'" 0 120; wait_db "SELECT status FROM user_apps WHERE id='$app_id'" running 180; wait_db "SELECT count(*) FROM app_routes WHERE user_app_id='$app_id'" 1 180
charge="$(db "SELECT count(*) FROM usage_charges WHERE user_id='$user_id' AND user_app_id='$app_id' AND usage_code='$usage_code' AND window_start='$window_start' AND amount_cents=100")"; ledger="$(db "SELECT count(*) FROM wallet_ledger_entries l JOIN usage_charges c ON c.wallet_ledger_entry_id=l.id WHERE c.user_id='$user_id' AND c.user_app_id='$app_id' AND c.usage_code='$usage_code' AND c.window_start='$window_start' AND l.business_type='usage'")"; bill="$(db "SELECT count(*) FROM bill_items i JOIN usage_charges c ON c.id=i.usage_charge_id WHERE c.user_id='$user_id' AND c.user_app_id='$app_id' AND c.usage_code='$usage_code' AND c.window_start='$window_start' AND i.amount_cents=100")"; mismatches="$(db "SELECT count(*) FROM bills b WHERE b.user_id='$user_id' AND b.total_cents<>(SELECT coalesce(sum(i.amount_cents),0) FROM bill_items i WHERE i.bill_id=b.id)")"; notices="$(db "SELECT string_agg(kind,',' ORDER BY kind) FROM user_notifications WHERE user_id='$user_id' AND (event_key IN (SELECT 'low-balance/'||id::text FROM usage_aggregates WHERE usage_code='$usage_code' AND window_start='$low_start') OR event_key IN (SELECT 'billing-suspended/'||id::text FROM usage_aggregates WHERE usage_code='$usage_code' AND window_start='$window_start') OR event_key IN (SELECT 'billing-recovered/'||id::text FROM deployment_jobs WHERE user_app_id='$app_id' AND operation='billing_recovery' ORDER BY created_at DESC LIMIT 1))")"
[[ "$charge|$ledger|$bill|$mismatches|$notices" == '1|1|1|0|billing_recovered,billing_suspended,low_balance' ]] || { echo "billing invariants failed: $charge|$ledger|$bill|$mismatches|$notices" >&2; exit 1; }
api_json GET /billing/usage "$user_token" | jq -e --arg code "$unpriced_code" '.usage|any(.usageCode==$code and .billingDisposition=="unpriced" and .amountCents==null)' >/dev/null
echo 'Billing warning, unpriced sealing, suspension, route removal, idempotent charge, statement snapshot, notification and automatic recovery verification passed'
