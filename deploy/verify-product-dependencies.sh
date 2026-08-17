#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
COMPOSE=(docker compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME"); fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
if [[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]]; then COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE"); fi
PORT="${PLATFORM_PORT:-8080}"
API="http://127.0.0.1:$PORT/api"
IMAGE='nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10'

db() {
  "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'
}
db_exec() { "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" >/dev/null; }
session() {
  local user_id="$1" token id
  token="$(openssl rand -hex 32)"
  id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$user_id',digest('$token','sha256'),now()+interval '30 minutes') RETURNING id")"
  printf '%s|%s\n' "$id" "$token"
}
status() {
  local method uri token body
  local -a args
  method="$1"
  uri="$2"
  token="${3:-}"
  body="${4:-}"
  args=(-sS -o /dev/null -w '%{http_code}' -X "$method")
  [[ -z "$token" ]] || args+=(-H "Authorization: Bearer $token")
  [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body")
  curl "${args[@]}" "$uri"
}
wait_job() {
  local job_id="$1" expected="${2:-succeeded}" deadline=$((SECONDS+120)) state=''
  while (( SECONDS < deadline )); do
    state="$(db "SELECT state FROM deployment_jobs WHERE id='$job_id'")"
    [[ "$state" == "$expected" ]] && return 0
    if [[ "$state" == failed && "$expected" != failed ]]; then
      echo "deployment failed: $(db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$job_id'")" >&2
      return 1
    fi
    sleep 0.5
  done
  echo "timed out waiting for deployment $job_id to reach $expected; last state was $state" >&2
  return 1
}
wait_product_test() {
  local test_id="$1" deadline=$((SECONDS+120)) state=''
  while (( SECONDS < deadline )); do
    state="$(db "SELECT state FROM app_product_version_tests WHERE id='$test_id'")"
    [[ "$state" == succeeded ]] && return 0
    if [[ "$state" == failed ]]; then
      echo "product test failed: $(db "SELECT coalesce(last_error,'') FROM app_product_version_tests WHERE id='$test_id'")" >&2
      return 1
    fi
    sleep 0.5
  done
  echo "timed out waiting for product test $test_id; last state was $state" >&2
  return 1
}
version_body() {
  local dependencies="${1:-[]}"
  jq -cn --arg image "$IMAGE" --argjson dependencies "$dependencies" '{
    imageDigest:$image,
    runtimeSpec:{cpuCores:0.25,memoryMiB:128,systemDiskGiB:1,env:{},secretKeys:[],volumes:[],dependencies:$dependencies},
    routeSpec:{containerPort:80,basePath:"/",stripPrefix:true,websocket:true,sse:true,cookiePath:"/"},
    healthSpec:{path:"/",intervalSeconds:5,timeoutSeconds:3},
    updateSpec:{dataPolicy:"stateless"}
  }'
}
publish_version() {
  local product_id="$1" version_id="$2" queued test_id
  queued="$(curl -fsS "${admin_auth[@]}" -d '{"secrets":{}}' "$API/admin/products/$product_id/versions/$version_id/tests")"
  test_id="$(jq -r .testId <<<"$queued")"
  wait_product_test "$test_id"
  curl -fsS -X POST -H "Authorization: Bearer $admin_token" "$API/admin/products/$product_id/versions/$version_id/publish" | jq -e '.published==true' >/dev/null
}

worker_id="$("${COMPOSE[@]}" ps -q worker)"
[[ -n "$worker_id" ]] || { echo 'worker must be running' >&2; exit 1; }
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$worker_id" | grep -Fxq 'DOCKER_EXECUTOR_ENABLED=true' || { echo 'dependency verification requires DOCKER_EXECUTOR_ENABLED=true' >&2; exit 1; }
runtime_owner="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$worker_id" | sed -n 's/^RUNTIME_OWNER=//p' | head -n 1)"
[[ -n "$runtime_owner" ]] || { echo 'worker must expose RUNTIME_OWNER for scoped verification' >&2; exit 1; }
runtime_scope="$(printf '%s' "$runtime_owner" | sha256sum | cut -c1-10)"
docker image inspect "$IMAGE" >/dev/null
admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
IFS='|' read -r admin_session_id admin_token < <(session "$admin_id")
admin_auth=(-H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json')
marker="$(openssl rand -hex 6)"
password="Dependency-$marker-Password!"
user_id='' user_session_id='' plan_id='' base_product_id='' dependent_product_id='' base_app_id='' dependent_app_id='' network=''
worker_stopped=false
stop_worker() { if [[ "$worker_stopped" == false ]]; then "${COMPOSE[@]}" stop worker >/dev/null; worker_stopped=true; fi; }
start_worker() { if [[ "$worker_stopped" == true ]]; then "${COMPOSE[@]}" start worker >/dev/null; worker_stopped=false; fi; }
cleanup() {
  set +e
  start_worker
  [[ -z "$user_session_id" ]] || db_exec "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$user_session_id'"
  db_exec "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session_id'"
  if [[ -n "$user_id" ]]; then
    db_exec "UPDATE deployment_jobs SET state='failed',last_error=coalesce(last_error,'dependency verification cleanup'),updated_at=now() WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id='$user_id') AND state NOT IN ('succeeded','failed'); DELETE FROM app_routes WHERE user_app_id IN (SELECT id FROM user_apps WHERE user_id='$user_id'); UPDATE user_apps SET status='suspended',suspension_reason=NULL WHERE user_id='$user_id'; UPDATE users SET status='suspended',updated_at=now() WHERE id='$user_id'"
    for verified_app_id in "$base_app_id" "$dependent_app_id"; do
      [[ -z "$verified_app_id" ]] && continue
      while IFS= read -r container; do [[ -z "$container" ]] || docker rm -f "$container" >/dev/null 2>&1; done < <(docker ps -a --filter "label=cloudmeter.owner=$runtime_owner" --filter "label=cloudmeter.app_id=$verified_app_id" --format '{{.Names}}')
    done
    if [[ -n "$network" ]]; then
      router_id="$("${COMPOSE[@]}" ps -q app-router)"; proxy_id="$("${COMPOSE[@]}" ps -q egress-proxy)"
      [[ -z "$router_id" ]] || docker network disconnect -f "$network" "$router_id" >/dev/null 2>&1
      [[ -z "$proxy_id" ]] || docker network disconnect -f "$network" "$proxy_id" >/dev/null 2>&1
      docker network rm "$network" >/dev/null 2>&1
    fi
  fi
  [[ -z "$plan_id" ]] || db_exec "UPDATE plans SET purchase_enabled=false WHERE id='$plan_id'"
  if [[ -n "$base_product_id" || -n "$dependent_product_id" ]]; then
    db_exec "UPDATE app_products SET status='retired' WHERE id IN (nullif('$base_product_id','')::uuid,nullif('$dependent_product_id','')::uuid)"
  fi
}
trap cleanup EXIT

base_product_id="$(curl -fsS "${admin_auth[@]}" -d "{\"slug\":\"verify-foundation-$marker\",\"name\":\"Dependency foundation $marker\"}" "$API/admin/products" | jq -r .id)"
dependent_product_id="$(curl -fsS "${admin_auth[@]}" -d "{\"slug\":\"verify-dependent-$marker\",\"name\":\"Dependency consumer $marker\"}" "$API/admin/products" | jq -r .id)"
unknown_id="$(cat /proc/sys/kernel/random/uuid)"
unknown_dependencies="$(jq -cn --arg id "$unknown_id" '[{key:"unknown",productId:$id,serviceSlug:"unknown",required:true}]')"
[[ "$(status POST "$API/admin/products/$dependent_product_id/versions" "$admin_token" "$(version_body "$unknown_dependencies")")" == 400 ]] || { echo 'unknown dependency product was accepted' >&2; exit 1; }
self_dependencies="$(jq -cn --arg id "$base_product_id" '[{key:"self",productId:$id,serviceSlug:"foundation",required:true}]')"
[[ "$(status POST "$API/admin/products/$base_product_id/versions" "$admin_token" "$(version_body "$self_dependencies")")" == 400 ]] || { echo 'self dependency was accepted' >&2; exit 1; }
malformed_dependencies="$(jq -cn --arg id "$unknown_id" '[{key:"bad",productId:$id,serviceSlug:"bad",required:true,url:"http://invalid"}]')"
[[ "$(status POST "$API/admin/products/$dependent_product_id/versions" "$admin_token" "$(version_body "$malformed_dependencies")")" == 400 ]] || { echo 'dependency with undeclared fields was accepted' >&2; exit 1; }

base_version_id="$(curl -fsS "${admin_auth[@]}" -d "$(version_body)" "$API/admin/products/$base_product_id/versions" | jq -r .id)"
publish_version "$base_product_id" "$base_version_id"
dependency="$(jq -cn --arg id "$base_product_id" '[{key:"foundation",productId:$id,serviceSlug:"foundation",required:true}]')"
dependent_version_id="$(curl -fsS "${admin_auth[@]}" -d "$(version_body "$dependency")" "$API/admin/products/$dependent_product_id/versions" | jq -r .id)"
publish_version "$dependent_product_id" "$dependent_version_id"
cycle="$(jq -cn --arg id "$dependent_product_id" '[{key:"consumer",productId:$id,serviceSlug:"consumer",required:true}]')"
[[ "$(status POST "$API/admin/products/$base_product_id/versions" "$admin_token" "$(version_body "$cycle")")" == 400 ]] || { echo 'product dependency cycle was accepted' >&2; exit 1; }

plan_id="$(curl -fsS "${admin_auth[@]}" -d "{\"code\":\"verify-dependency-$marker\",\"name\":\"Dependency verification $marker\"}" "$API/admin/plans" | jq -r .id)"
plan_body="$(jq -cn --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg base "$base_product_id" --arg consumer "$dependent_product_id" '{cyclePriceCents:0,apps:4,cpuCores:2,memoryGiB:2,systemDiskGiB:10,dataDiskGiB:0,backupStorageGiB:0,backupOperationsPerMonth:0,concurrentDeployments:2,publicIngresses:4,ingressOverageEnabled:false,egressGiB:1,egressOverageEnabled:false,creditGrantCents:0,allowedProductIds:[$base,$consumer],effectiveAt:$now}')"
plan_version_id="$(curl -fsS "${admin_auth[@]}" -d "$plan_body" "$API/admin/plans/$plan_id/versions" | jq -r .id)"
user_id="$(curl -fsS "${admin_auth[@]}" -d "{\"email\":\"dependency-$marker@example.invalid\",\"password\":\"$password\",\"displayName\":\"Dependency verification\",\"role\":\"user\"}" "$API/admin/users" | jq -r .id)"
curl -fsS "${admin_auth[@]}" -X PUT -d "{\"planVersionId\":\"$plan_version_id\",\"endsAt\":null}" "$API/admin/users/$user_id/subscription" >/dev/null
curl -fsS "${admin_auth[@]}" -d "{\"amountCents\":1000,\"businessRef\":\"dependency-verify/$marker\",\"note\":\"Product dependency verification\"}" "$API/admin/users/$user_id/wallet/adjust" >/dev/null
IFS='|' read -r user_session_id user_token < <(session "$user_id")
user_auth=(-H "Authorization: Bearer $user_token" -H 'Content-Type: application/json')
if [[ "${runtime_owner,,}" == cloudmeter ]]; then
  network="user_net_$user_id"
else
  network="user_net_${runtime_scope}-${user_id}"
fi

catalog="$(curl -fsS "${user_auth[@]}" "$API/products")"
jq -e --arg product "$dependent_product_id" --arg version "$dependent_version_id" '.products[] | select(.id==$product and .versionId==$version) | (.deployable==false and (.missingDependencies | index("foundation (foundation)") != null))' <<<"$catalog" >/dev/null
consumer_create="$(jq -cn --arg product "$dependent_product_id" --arg version "$dependent_version_id" --arg key "dependency-missing/$marker" '{productId:$product,versionId:$version,slug:"consumer",idempotencyKey:$key,secrets:{}}')"
[[ "$(status POST "$API/apps" "$user_token" "$consumer_create")" == 409 ]] || { echo 'dependent application was accepted before its required service was running' >&2; exit 1; }
base_create="$(jq -cn --arg product "$base_product_id" --arg version "$base_version_id" --arg key "dependency-base/$marker" '{productId:$product,versionId:$version,slug:"foundation",idempotencyKey:$key,secrets:{}}')"
base_deployment="$(curl -fsS "${user_auth[@]}" -d "$base_create" "$API/apps")"
base_app_id="$(jq -r .appId <<<"$base_deployment")"
wait_job "$(jq -r .jobId <<<"$base_deployment")"
ready_catalog="$(curl -fsS "${user_auth[@]}" "$API/products")"
jq -e --arg product "$dependent_product_id" --arg version "$dependent_version_id" '.products[] | select(.id==$product and .versionId==$version) | (.deployable==true and (.missingDependencies|length)==0)' <<<"$ready_catalog" >/dev/null
consumer_create="$(jq -c --arg key "dependency-consumer/$marker" '.idempotencyKey=$key' <<<"$consumer_create")"
dependent_deployment="$(curl -fsS "${user_auth[@]}" -d "$consumer_create" "$API/apps")"
dependent_app_id="$(jq -r .appId <<<"$dependent_deployment")"
wait_job "$(jq -r .jobId <<<"$dependent_deployment")"
dependent_container="$(db "SELECT upstream_container FROM app_routes WHERE user_app_id='$dependent_app_id'")"
upper="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$dependent_container" | sed -n 's/^NO_PROXY=//p' | head -n 1)"
lower="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$dependent_container" | sed -n 's/^no_proxy=//p' | head -n 1)"
tr ',' '\n' <<<"$upper" | grep -Fxq foundation
tr ',' '\n' <<<"$lower" | grep -Fxq foundation
docker exec "$dependent_container" wget -Y off -q -T 5 -O - http://foundation/ | grep -Fq 'Welcome to nginx'

bash "$ROOT/deploy/verify-runtime-isolation.sh"

stop_worker
queued_update="$(curl -fsS "${user_auth[@]}" -d "$(jq -cn --arg version "$dependent_version_id" --arg key "dependency-race/$marker" '{versionId:$version,idempotencyKey:$key}')" "$API/apps/$dependent_app_id/releases")"
base_container="$(db "SELECT upstream_container FROM app_routes WHERE user_app_id='$base_app_id'")"
docker rm -f "$base_container" >/dev/null
start_worker
queued_job_id="$(jq -r .jobId <<<"$queued_update")"
wait_job "$queued_job_id" failed
[[ "$(db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$queued_job_id'")" == *'required dependencies are unavailable'* ]]
[[ "$(db "SELECT status FROM user_apps WHERE id='$dependent_app_id'")" == running ]]
base_recovery="$(curl -fsS "${user_auth[@]}" -d "$(jq -cn --arg version "$base_version_id" --arg key "dependency-recovery/$marker" '{versionId:$version,idempotencyKey:$key}')" "$API/apps/$base_app_id/releases")"
wait_job "$(jq -r .jobId <<<"$base_recovery")"
recovered_container="$(db "SELECT upstream_container FROM app_routes WHERE user_app_id='$base_app_id'")"
[[ "$recovered_container" != "$base_container" ]]
docker exec "$dependent_container" wget -Y off -q -T 5 -O - http://foundation/ | grep -Fq 'Welcome to nginx'

echo 'Product dependency validation, catalog readiness, deployment gate, queued recheck, stable alias and NO_PROXY verification passed'
