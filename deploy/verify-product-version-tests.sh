#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
COMPOSE=(docker compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then
  COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME")
fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
if [[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]]; then
  COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE")
fi
PORT="${PLATFORM_PORT:-8080}"
API="http://127.0.0.1:$PORT/api"
IMAGE='nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10'
LATEST_MIGRATION="$(find migrations -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -n 1)"

db() {
  "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'
}

db_exec() {
  "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" >/dev/null
}

status() {
  local method="$1" uri="$2" token="${3:-}" body="${4:-}"
  local args=(-sS -o /dev/null -w '%{http_code}' -X "$method")
  [[ -z "$token" ]] || args+=(-H "Authorization: Bearer $token")
  [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body")
  curl "${args[@]}" "$uri"
}

expect_db_failure() {
  local query="$1" expected="$2" output code
  set +e
  output="$("${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c "$query" 2>&1)"
  code=$?
  set -e
  if (( code == 0 )) || [[ "$output" != *"$expected"* ]]; then
    echo "database mutation was not rejected as expected: $output" >&2
    return 1
  fi
}

wait_db() {
  local query="$1" expected="$2" seconds="${3:-90}" deadline value=''
  deadline=$((SECONDS+seconds))
  while (( SECONDS < deadline )); do
    value="$(db "$query")"
    [[ "$value" == "$expected" ]] && return 0
    if [[ "$value" == failed && "$expected" != failed ]]; then
      echo "product test failed: $(db "SELECT coalesce(last_error,'') FROM app_product_version_tests WHERE id='$test_id'")" >&2
      return 1
    fi
    sleep 0.5
  done
  echo "timed out waiting for $expected; last value was $value" >&2
  return 1
}

wait_runtime_absent() {
  local container="$1" network="$2" seconds="${3:-30}" deadline
  deadline=$((SECONDS+seconds))
  while (( SECONDS < deadline )); do
    if ! docker container inspect "$container" >/dev/null 2>&1 && ! docker network inspect "$network" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "runtime cleanup timed out for $container and $network" >&2
  return 1
}

worker_id="$("${COMPOSE[@]}" ps -q worker)"
[[ -n "$worker_id" ]] || { echo 'worker must be running' >&2; exit 1; }
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$worker_id" | grep -Fxq 'DOCKER_EXECUTOR_ENABLED=true' || {
  echo 'product-version runtime verification requires DOCKER_EXECUTOR_ENABLED=true' >&2
  exit 1
}
runtime_owner="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$worker_id" | sed -n 's/^RUNTIME_OWNER=//p' | head -n 1)"
[[ -n "$runtime_owner" ]] || { echo 'worker must expose RUNTIME_OWNER for scoped verification' >&2; exit 1; }
runtime_scope="$(printf '%s' "$runtime_owner" | sha256sum | cut -c1-10)"
docker image inspect "$IMAGE" >/dev/null
[[ "$(db "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations")" == "${LATEST_MIGRATION}|clean" ]] || { echo "migration ${LATEST_MIGRATION} must be applied before verification" >&2; exit 1; }

admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
admin_token="$(openssl rand -hex 32)"
admin_session_id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$admin_id',digest('$admin_token','sha256'),now()+interval '20 minutes') RETURNING id")"
admin_auth=(-H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json')

marker="$(openssl rand -hex 6)"
secret_value="product-test-secret-$marker"
product_id=''
version_id=''
test_id=''
test_container=''
test_network=''
orphan_id=''
orphan_container=''
orphan_probe=''
orphan_network=''
worker_stopped=false

stop_worker() {
  if [[ "$worker_stopped" == false ]]; then
    "${COMPOSE[@]}" stop worker >/dev/null
    worker_stopped=true
  fi
}

start_worker() {
  if [[ "$worker_stopped" == true ]]; then
    "${COMPOSE[@]}" start worker >/dev/null
    worker_stopped=false
  fi
}

cleanup() {
  set +e
  start_worker
  [[ -z "$test_container" ]] || docker rm -f "$test_container" >/dev/null 2>&1
  [[ -z "$test_id" ]] || docker rm -f "cm-test-health-$runtime_scope-$test_id" >/dev/null 2>&1
  if [[ -n "$test_id" ]]; then
    local compact="${test_id//-/}"
    docker rm -f "cm-test-health-$runtime_scope-${compact:0:12}" >/dev/null 2>&1
  fi
  [[ -z "$test_network" ]] || docker network rm "$test_network" >/dev/null 2>&1
  [[ -z "$orphan_container" ]] || docker rm -f "$orphan_container" >/dev/null 2>&1
  [[ -z "$orphan_probe" ]] || docker rm -f "$orphan_probe" >/dev/null 2>&1
  [[ -z "$orphan_network" ]] || docker network rm "$orphan_network" >/dev/null 2>&1
  db_exec "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session_id'" >/dev/null 2>&1
  [[ -z "$product_id" ]] || db_exec "UPDATE app_products SET status='retired' WHERE id='$product_id'" >/dev/null 2>&1
}
trap cleanup EXIT

[[ "$(status POST "$API/admin/products" "$admin_token" '{"slug":"INVALID_slug","name":"Invalid product"}')" == 400 ]] || {
  echo 'invalid product slug was accepted' >&2
  exit 1
}
product_body="$(jq -cn --arg slug "verify-product-$marker" --arg name "Product test verification $marker" '{slug:$slug,name:$name}')"
product_id="$(curl -fsS "${admin_auth[@]}" -d "$product_body" "$API/admin/products" | jq -r .id)"
[[ "$product_id" =~ ^[0-9a-f-]{36}$ ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.create' AND resource_id='$product_id'")" == 1 ]]
updated_name="Product lifecycle verification $marker"
update_body="$(jq -cn --arg name "$updated_name" '{name:$name}')"
updated="$(curl -fsS -X PATCH "${admin_auth[@]}" -d "$update_body" "$API/admin/products/$product_id")"
updated_replay="$(curl -fsS -X PATCH "${admin_auth[@]}" -d "$update_body" "$API/admin/products/$product_id")"
[[ "$(jq -r .name <<<"$updated")" == "$updated_name" && "$(jq -r .idempotent <<<"$updated")" == false && "$(jq -r .idempotent <<<"$updated_replay")" == true ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.update' AND resource_id='$product_id'")" == 1 ]]
expect_db_failure "UPDATE app_products SET id=gen_random_uuid() WHERE id='$product_id'" 'product identity is immutable'
expect_db_failure "UPDATE app_products SET slug='changed-$marker' WHERE id='$product_id'" 'product identity is immutable'
expect_db_failure "UPDATE app_products SET created_at=created_at+interval '1 second' WHERE id='$product_id'" 'product identity is immutable'
expect_db_failure "DELETE FROM app_products WHERE id='$product_id'" 'immutable history cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_products CASCADE; ROLLBACK;' 'immutable history cannot be truncated'

version_body="$(jq -cn --arg image "$IMAGE" '{
  imageDigest:$image,
  runtimeSpec:{cpuCores:0.25,memoryMiB:128,systemDiskGiB:1,env:{VERIFY_MODE:"acceptance"},secretKeys:["VERIFY_SECRET"],volumes:[{name:"data",mountPath:"/data",sizeGiB:1}]},
  routeSpec:{containerPort:80,basePath:"/",websocket:true,sse:true},
  healthSpec:{path:"/",intervalSeconds:10,timeoutSeconds:3},
  updateSpec:{dataPolicy:"volume_compatible"}
}')"
version_id="$(curl -fsS "${admin_auth[@]}" -d "$version_body" "$API/admin/products/$product_id/versions" | jq -r .id)"
[[ "$version_id" =~ ^[0-9a-f-]{36}$ ]]

[[ "$(status POST "$API/admin/products/$product_id/versions/$version_id/publish" "$admin_token")" == 409 ]] || {
  echo 'untested version was published through the API' >&2
  exit 1
}
expect_db_failure "UPDATE app_product_versions SET published_at=now() WHERE id='$version_id'" 'successful product version test is required'
[[ "$(status POST "$API/admin/products/$product_id/versions/$version_id/tests" "$admin_token" '{"secrets":{}}')" == 400 ]] || {
  echo 'missing required test secret was accepted' >&2
  exit 1
}

stop_worker
test_body="$(jq -cn --arg value "$secret_value" '{secrets:{VERIFY_SECRET:$value}}')"
queued="$(curl -fsS "${admin_auth[@]}" -d "$test_body" "$API/admin/products/$product_id/versions/$version_id/tests")"
test_id="$(jq -r .testId <<<"$queued")"
[[ "$test_id" =~ ^[0-9a-f-]{36}$ && "$(jq -r .state <<<"$queued")" == queued ]]
[[ "$(status PATCH "$API/admin/products/$product_id/availability" "$admin_token" '{"enabled":false}')" == 409 ]] || {
  echo 'product was retired while a version test was active' >&2
  exit 1
}
test_container="cm-test-$runtime_scope-$test_id"
test_network="cm-test-net-$runtime_scope-${test_id//-/}"

encrypted="$(db "SELECT encrypted_secrets->>'VERIFY_SECRET' FROM app_product_version_tests WHERE id='$test_id'")"
[[ "$encrypted" == cmsec:v1:* && "$encrypted" != *"$secret_value"* ]] || {
  echo 'test secret was not stored as authenticated ciphertext' >&2
  exit 1
}
expect_db_failure "UPDATE app_product_version_tests SET id=gen_random_uuid() WHERE id='$test_id'" 'product version test snapshot is immutable'

start_worker
wait_db "SELECT state FROM app_product_version_tests WHERE id='$test_id'" health_checking 120
stop_worker

docker container inspect "$test_container" >/dev/null
[[ "$(docker inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$test_container")" == no ]]
[[ "$(docker inspect --format '{{.HostConfig.Privileged}}' "$test_container")" == false ]]
[[ "$(docker inspect --format '{{len .NetworkSettings.Networks}}' "$test_container")" == 1 ]]
docker inspect --format '{{range .HostConfig.SecurityOpt}}{{println .}}{{end}}' "$test_container" | grep -Fxq 'no-new-privileges:true'
docker inspect "$test_container" | jq -e --arg value "$secret_value" '.[0].Config.Env | index("VERIFY_SECRET="+$value) != null' >/dev/null
docker inspect --format '{{json .HostConfig.Tmpfs}}' "$test_container" | jq -e 'has("/data")' >/dev/null
[[ "$(docker network inspect --format '{{.Internal}}' "$test_network")" == true ]]

start_worker
wait_db "SELECT state FROM app_product_version_tests WHERE id='$test_id'" succeeded 120
[[ "$(db "SELECT encrypted_secrets::text FROM app_product_version_tests WHERE id='$test_id'")" == '{}' ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.test.request' AND resource_id='$version_id'")" == 1 ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.test.succeeded' AND resource_id='$test_id'")" == 1 ]]
expect_db_failure "UPDATE app_product_version_tests SET attempts=attempts WHERE id='$test_id'" 'completed product version tests are immutable'
expect_db_failure "DELETE FROM app_product_version_tests WHERE id='$test_id'" 'immutable history cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_product_version_tests CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
wait_runtime_absent "$test_container" "$test_network" 30

first_publish="$(curl -fsS -X POST -H "Authorization: Bearer $admin_token" "$API/admin/products/$product_id/versions/$version_id/publish")"
[[ "$(jq -r .published <<<"$first_publish")" == true && "$(jq -r .alreadyPublished <<<"$first_publish")" == false ]]
second_publish="$(curl -fsS -X POST -H "Authorization: Bearer $admin_token" "$API/admin/products/$product_id/versions/$version_id/publish")"
[[ "$(jq -r .published <<<"$second_publish")" == true && "$(jq -r .alreadyPublished <<<"$second_publish")" == true ]]
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.publish' AND resource_id='$version_id'")" == 1 ]]
[[ "$(db "SELECT p.status || '|' || (pv.published_at IS NOT NULL)::text FROM app_products p JOIN app_product_versions pv ON pv.product_id=p.id WHERE p.id='$product_id' AND pv.id='$version_id'")" == 'published|true' ]]
[[ "$(status POST "$API/admin/products/$product_id/versions/$version_id/tests" "$admin_token" "$test_body")" == 409 ]]
expect_db_failure "UPDATE app_product_versions SET id=gen_random_uuid() WHERE id='$version_id'" 'product version configuration is immutable'
expect_db_failure "UPDATE app_product_versions SET published_at=NULL WHERE id='$version_id'" 'published product version cannot be unpublished'
expect_db_failure "DELETE FROM app_product_versions WHERE id='$version_id'" 'immutable history cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_product_versions CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.version.create' AND resource_id='$version_id'")" == 1 ]]

retired="$(curl -fsS -X PATCH "${admin_auth[@]}" -d '{"enabled":false}' "$API/admin/products/$product_id/availability")"
retired_replay="$(curl -fsS -X PATCH "${admin_auth[@]}" -d '{"enabled":false}' "$API/admin/products/$product_id/availability")"
[[ "$(jq -r .status <<<"$retired")" == retired && "$(jq -r .idempotent <<<"$retired")" == false && "$(jq -r .idempotent <<<"$retired_replay")" == true ]]
curl -fsS -H "Authorization: Bearer $admin_token" "$API/products" | jq -e --arg product "$product_id" '[.products[] | select(.id==$product)] | length == 0' >/dev/null
[[ "$(status POST "$API/admin/products/$product_id/versions" "$admin_token" "$version_body")" == 409 ]] || {
  echo 'retired product accepted a new version' >&2
  exit 1
}
restored="$(curl -fsS -X PATCH "${admin_auth[@]}" -d '{"enabled":true}' "$API/admin/products/$product_id/availability")"
restored_replay="$(curl -fsS -X PATCH "${admin_auth[@]}" -d '{"enabled":true}' "$API/admin/products/$product_id/availability")"
[[ "$(jq -r .status <<<"$restored")" == published && "$(jq -r .idempotent <<<"$restored")" == false && "$(jq -r .idempotent <<<"$restored_replay")" == true ]]
curl -fsS -H "Authorization: Bearer $admin_token" "$API/products" | jq -e --arg product "$product_id" --arg version "$version_id" '[.products[] | select(.id==$product and .versionId==$version)] | length == 1' >/dev/null
[[ "$(db "SELECT count(*) FROM audit_logs WHERE action='product.availability.update' AND resource_id='$product_id'")" == 2 ]]

orphan_id="$(cat /proc/sys/kernel/random/uuid)"
orphan_container="cm-test-$runtime_scope-$orphan_id"
orphan_probe="cm-test-health-$runtime_scope-$orphan_id"
orphan_network="cm-test-net-$runtime_scope-${orphan_id//-/}"
docker network create --internal --label cloudmeter.managed=true --label "cloudmeter.owner=$runtime_owner" "$orphan_network" >/dev/null
docker create --name "$orphan_container" --label cloudmeter.managed=true --label "cloudmeter.owner=$runtime_owner" --network "$orphan_network" "$IMAGE" >/dev/null
docker create --name "$orphan_probe" --label cloudmeter.managed=true --label "cloudmeter.owner=$runtime_owner" --network "$orphan_network" "$IMAGE" >/dev/null
wait_runtime_absent "$orphan_container" "$orphan_network" 30
if docker container inspect "$orphan_probe" >/dev/null 2>&1; then
  echo 'orphan health probe was not reclaimed' >&2
  exit 1
fi

echo 'Product lifecycle, pre-publish test, encrypted secret, isolated runtime, immutable snapshot, idempotent publish, audit and orphan cleanup verification passed'
