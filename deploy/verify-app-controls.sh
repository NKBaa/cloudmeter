#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DOCKER_BIN="${DOCKER_BIN:-docker}"
COMPOSE=("$DOCKER_BIN" compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME"); fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
if [[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]]; then COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE"); fi

PLATFORM_HOST="${PLATFORM_HOST:-127.0.0.1}"
PLATFORM_PORT="${PLATFORM_PORT:-8080}"
API="http://$PLATFORM_HOST:$PLATFORM_PORT/api"
BASE_URL="http://$PLATFORM_HOST:$PLATFORM_PORT"
IMAGE='nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10'
LATEST_MIGRATION="$(find migrations -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -n 1)"
RESPONSE_FILE="$(mktemp)"

compose() { "${COMPOSE[@]}" "$@"; }

# Keep container-internal paths such as /data intact under Git Bash/MSYS.
docker() { MSYS_NO_PATHCONV=1 command "$DOCKER_BIN" "$@"; }

db() {
  compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'
}

db_exec() {
  compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" >/dev/null
}

db_quiet() { db_exec "$1" >/dev/null 2>&1 || true; }

expect_db_failure() {
  local query="$1" expected="$2" output code
  set +e
  output="$(compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c "$query" 2>&1)"
  code=$?
  set -e
  if (( code == 0 )) || [[ "$output" != *"$expected"* ]]; then
    echo "database mutation was not rejected as expected: $output" >&2
    return 1
  fi
}

new_session() {
  local user_id="$1" token id
  token="$(openssl rand -hex 32)"
  id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$user_id',digest('$token','sha256'),now()+interval '30 minutes') RETURNING id")"
  printf '%s|%s\n' "$id" "$token"
}

request() {
  local method="$1" uri="$2" token="${3:-}" body="${4:-}"
  local -a args=(-sS -o "$RESPONSE_FILE" -w '%{http_code}' -X "$method")
  [[ -z "$token" ]] || args+=(-H "Authorization: Bearer $token")
  [[ -z "$body" ]] || args+=(-H 'Content-Type: application/json' -d "$body")
  RESPONSE_STATUS="$(curl "${args[@]}" "$uri")"
}

response_error_code() { jq -r '.error.code // empty' "$RESPONSE_FILE" 2>/dev/null || true; }
response_value() { jq -r "$1" "$RESPONSE_FILE"; }

docker_object_exists() { docker "$@" >/dev/null 2>&1; }

wait_value() {
  local query="$1" expected="$2" seconds="${3:-180}" deadline value=''
  deadline="$((SECONDS + seconds))"
  while (( SECONDS < deadline )); do
    value="$(db "$query")"
    [[ "$value" == "$expected" ]] && return 0
    sleep 0.5
  done
  echo "timed out waiting for '$expected'; last value was '$value'" >&2
  return 1
}

wait_job() {
  local job_id="$1" seconds="${2:-180}" deadline state=''
  deadline="$((SECONDS + seconds))"
  while (( SECONDS < deadline )); do
    state="$(db "SELECT state FROM deployment_jobs WHERE id='$job_id'")"
    [[ "$state" == succeeded ]] && return 0
    if [[ "$state" == failed ]]; then
      echo "deployment failed: $(db "SELECT coalesce(last_error,'') FROM deployment_jobs WHERE id='$job_id'")" >&2
      return 1
    fi
    sleep 0.5
  done
  echo "timed out waiting for deployment $job_id; last state was $state" >&2
  return 1
}

wait_product_test() {
  local test_id="$1" seconds="${2:-180}" deadline state=''
  deadline="$((SECONDS + seconds))"
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

wait_backup() {
  local backup_id="$1" seconds="${2:-180}" deadline state=''
  deadline="$((SECONDS + seconds))"
  while (( SECONDS < deadline )); do
    state="$(db "SELECT status FROM app_backups WHERE id='$backup_id'")"
    [[ "$state" == succeeded ]] && return 0
    if [[ "$state" == failed ]]; then
      echo "backup failed: $(db "SELECT coalesce(last_error,'') FROM app_backups WHERE id='$backup_id'")" >&2
      return 1
    fi
    sleep 0.5
  done
  echo "timed out waiting for backup $backup_id; last state was $state" >&2
  return 1
}

wait_restore() {
  local restore_id="$1" seconds="${2:-180}" deadline state=''
  deadline="$((SECONDS + seconds))"
  while (( SECONDS < deadline )); do
    state="$(db "SELECT status FROM app_restore_jobs WHERE id='$restore_id'")"
    [[ "$state" == succeeded ]] && return 0
    if [[ "$state" == failed ]]; then
      echo "restore failed: $(db "SELECT coalesce(last_error,'') FROM app_restore_jobs WHERE id='$restore_id'")" >&2
      return 1
    fi
    sleep 0.5
  done
  echo "timed out waiting for restore $restore_id; last state was $state" >&2
  return 1
}

APP_COOKIE_JAR="$(mktemp)"

establish_app_access() {
  local app="$1" token="$2"
  rm -f "$APP_COOKIE_JAR"
  curl -sS -o /dev/null -c "$APP_COOKIE_JAR" -X POST \
    -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
    --data '{}' "$API/apps/$app/access" || return 1
}

wait_http() {
  local uri="$1" expected="$2" seconds="${3:-45}" deadline
  deadline="$((SECONDS + seconds))"
  while (( SECONDS < deadline )); do
    if [[ -f "$APP_COOKIE_JAR" ]]; then
      RESPONSE_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -b "$APP_COOKIE_JAR" "$uri")"
    else
      request GET "$uri"
    fi
    [[ "$RESPONSE_STATUS" == "$expected" ]] && return 0
    sleep 0.5
  done
  echo "timed out waiting for HTTP $expected at $uri; last status was $RESPONSE_STATUS" >&2
  return 1
}

runtime_scope() {
  local owner="$1"
  owner="$(sed -E 's/^[[:space:]]+|[[:space:]]+$//g' <<<"$owner")"
  printf '%s' "$owner" | sha256sum | cut -c1-10
}

app_volume_name() {
  local owner="$1" app_id="$2" key="$3" compact
  compact="${app_id//-/}"
  compact="${compact:0:20}"
  if [[ "${owner,,}" == cloudmeter ]]; then
    printf 'cmv-%s-%s\n' "$compact" "$key"
  else
    printf 'cmv-%s-%s-%s\n' "$(runtime_scope "$owner")" "$compact" "$key"
  fi
}

backup_volume_name() {
  local owner="$1" configured="$2" scope
  configured="$(sed -E 's/^[[:space:]]+|[[:space:]]+$//g' <<<"$configured")"
  [[ -n "$configured" ]] || configured='cloudmeter_backup_data'
  if [[ "${owner,,}" == cloudmeter ]]; then
    printf '%s\n' "$configured"
    return
  fi
  scope="$(runtime_scope "$owner")"
  if [[ "$configured" == cloudmeter_backup_data ]]; then
    printf 'cloudmeter_backup_%s\n' "$scope"
  elif [[ "$configured" == *"-$scope" || "$configured" == *"_$scope" ]]; then
    printf '%s\n' "$configured"
  else
    printf '%s-%s\n' "$configured" "$scope"
  fi
}

worker_stopped=false
stop_worker() {
  if [[ "$worker_stopped" == false ]]; then
    compose stop worker >/dev/null
    worker_stopped=true
  fi
}

start_worker() {
  if [[ "$worker_stopped" == true ]]; then
    compose start worker >/dev/null
    worker_stopped=false
  fi
}

worker_id="$(compose ps -q worker)"
[[ -n "$worker_id" ]] || { echo 'worker must be running' >&2; exit 1; }
worker_env="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$worker_id")"
grep -Fxq 'DOCKER_EXECUTOR_ENABLED=true' <<<"$worker_env" || { echo 'application control verification requires DOCKER_EXECUTOR_ENABLED=true' >&2; exit 1; }
runtime_owner="$(sed -n 's/^RUNTIME_OWNER=//p' <<<"$worker_env" | head -n 1)"
[[ -n "$runtime_owner" ]] || { echo 'worker must expose RUNTIME_OWNER for scoped verification' >&2; exit 1; }
backup_configured="$(sed -n 's/^BACKUP_STORAGE_VOLUME=//p' <<<"$worker_env" | head -n 1)"
[[ -n "$backup_configured" ]] || { echo 'worker must expose BACKUP_STORAGE_VOLUME for scoped verification' >&2; exit 1; }
runtime_scope_value="$(runtime_scope "$runtime_owner")"
backup_volume="$(backup_volume_name "$runtime_owner" "$backup_configured")"
docker image inspect "$IMAGE" >/dev/null
[[ "$(db "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations")" == "${LATEST_MIGRATION}|clean" ]] || { echo "migration ${LATEST_MIGRATION} must be applied before verification" >&2; exit 1; }

admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
IFS='|' read -r admin_session_id admin_token < <(new_session "$admin_id")
marker="$(openssl rand -hex 6)"
password="Lifecycle-$marker-Password!"
user_id='' user_session_id='' plan_id='' product_id='' app_id='' volume='' backup_id='' backup_storage_key='' restore_id=''
APP_COOKIE_JAR="$(mktemp)"

cleanup() {
  set +e
  start_worker
  if [[ -n "$backup_storage_key" && -n "$backup_volume" ]]; then
    docker run --rm -v "$backup_volume:/backup" "$IMAGE" sh -c "rm -f -- /backup/$backup_storage_key" >/dev/null 2>&1
  fi
  [[ -z "$user_session_id" ]] || db_quiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$user_session_id'"
  db_quiet "UPDATE sessions SET revoked_at=coalesce(revoked_at,now()) WHERE id='$admin_session_id'"
  if [[ -n "$app_id" ]]; then
    db_quiet "UPDATE deployment_jobs SET state='failed',last_error=coalesce(last_error,'application control verification cleanup'),updated_at=now() WHERE user_app_id='$app_id' AND state NOT IN ('succeeded','failed'); DELETE FROM app_routes WHERE user_app_id='$app_id'; UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE id='$app_id'"
    while IFS= read -r runtime_container; do
      [[ -z "$runtime_container" ]] || docker rm -f "$runtime_container" >/dev/null 2>&1
    done < <(docker ps -a --filter "label=cloudmeter.owner=$runtime_owner" --filter "label=cloudmeter.app_id=$app_id" --format '{{.Names}}')
  fi
  [[ -z "$user_id" ]] || db_quiet "UPDATE users SET status='suspended',updated_at=now() WHERE id='$user_id'"
  [[ -z "$plan_id" ]] || db_quiet "UPDATE plans SET purchase_enabled=false WHERE id='$plan_id'"
  [[ -z "$product_id" ]] || db_quiet "UPDATE app_products SET status='retired' WHERE id='$product_id'"
  rm -f "$RESPONSE_FILE" "$APP_COOKIE_JAR"
}
trap cleanup EXIT

request POST "$API/admin/products" "$admin_token" "$(jq -cn --arg slug "verify-control-$marker" --arg name "Application control $marker" '{slug:$slug,name:$name}')"
[[ "$RESPONSE_STATUS" == 201 ]] || { echo "product creation failed: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
product_id="$(response_value .id)"

version_body="$(jq -cn --arg image "$IMAGE" '{
  imageDigest:$image,
  runtimeSpec:{cpuCores:0.25,memoryMiB:128,systemDiskGiB:1,env:{},secretKeys:["ROTATION_KEY"],dependencies:[],volumes:[{name:"data",mountPath:"/data",sizeGiB:1}]},
  routeSpec:{containerPort:80,basePath:"/",stripPrefix:true,websocket:true,sse:true,cookiePath:"/"},
  healthSpec:{path:"/",intervalSeconds:2,timeoutSeconds:3},
  updateSpec:{dataPolicy:"volume_compatible"}
}')"
request POST "$API/admin/products/$product_id/versions" "$admin_token" "$version_body"
[[ "$RESPONSE_STATUS" == 201 ]] || { echo "product version creation failed: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
version_id="$(response_value .id)"
request POST "$API/admin/products/$product_id/versions/$version_id/tests" "$admin_token" "$(jq -cn --arg value "test-$marker" '{secrets:{ROTATION_KEY:$value}}')"
[[ "$RESPONSE_STATUS" == 202 ]] || { echo "product test queue failed: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
wait_product_test "$(response_value .testId)"
request POST "$API/admin/products/$product_id/versions/$version_id/publish" "$admin_token"
[[ "$RESPONSE_STATUS" == 200 && "$(response_value .published)" == true ]] || { echo 'product publication failed' >&2; exit 1; }

request POST "$API/admin/plans" "$admin_token" "$(jq -cn --arg code "verify-control-$marker" --arg name "Application control $marker" '{code:$code,name:$name}')"
[[ "$RESPONSE_STATUS" == 201 ]] || { echo 'plan creation failed' >&2; exit 1; }
plan_id="$(response_value .id)"
plan_version_body="$(jq -cn --arg product "$product_id" --arg effective "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '{cyclePriceCents:0,apps:2,cpuCores:2,memoryGiB:2,dataDiskGiB:2,backupStorageGiB:1,backupOperationsPerMonth:2,concurrentDeployments:2,publicIngresses:2,ingressOverageEnabled:false,egressGiB:1,egressOverageEnabled:false,creditGrantCents:0,allowedProductIds:[$product],effectiveAt:$effective}')"
request POST "$API/admin/plans/$plan_id/versions" "$admin_token" "$plan_version_body"
[[ "$RESPONSE_STATUS" == 201 ]] || { echo 'plan version creation failed' >&2; exit 1; }
plan_version_id="$(response_value .id)"

user_body="$(jq -cn --arg email "control-$marker@example.invalid" --arg password "$password" --arg name 'Application control verification' '{email:$email,password:$password,displayName:$name,role:"user"}')"
request POST "$API/admin/users" "$admin_token" "$user_body"
[[ "$RESPONSE_STATUS" == 201 ]] || { echo 'user creation failed' >&2; exit 1; }
user_id="$(response_value .id)"
request POST "$API/admin/users/$user_id/wallet/adjust" "$admin_token" "$(jq -cn --arg ref "control-funding/$marker" '{amountCents:100000,businessRef:$ref,note:"Application lifecycle verification funding"}')"
[[ "$RESPONSE_STATUS" == 200 ]] || { echo 'verification wallet funding failed' >&2; exit 1; }
request PUT "$API/admin/users/$user_id/subscription" "$admin_token" "$(jq -cn --arg plan "$plan_version_id" '{planVersionId:$plan,endsAt:null}')"
[[ "$RESPONSE_STATUS" == 200 ]] || { echo 'subscription assignment failed' >&2; exit 1; }
IFS='|' read -r user_session_id user_token < <(new_session "$user_id")

deploy_body="$(jq -cn --arg product "$product_id" --arg version "$version_id" --arg key "control-deploy/$marker" --arg secret "initial-$marker" '{productId:$product,versionId:$version,slug:"lifecycle",idempotencyKey:$key,secrets:{ROTATION_KEY:$secret}}')"
request POST "$API/apps" "$user_token" "$deploy_body"
[[ "$RESPONSE_STATUS" == 201 ]] || { echo "application deployment failed to queue: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
app_id="$(response_value .appId)"
deployment_job_id="$(response_value .jobId)"
wait_job "$deployment_job_id"
wait_value "SELECT status FROM user_apps WHERE id='$app_id'" running
[[ "$(db "SELECT operation FROM deployment_jobs WHERE id='$deployment_job_id'")" == deploy ]] || { echo 'initial deployment operation was not recorded' >&2; exit 1; }
source_release="$(db "SELECT last_successful_release_id FROM user_apps WHERE id='$app_id'")"
expect_db_failure "UPDATE app_releases SET release_number=release_number+1000 WHERE id='$source_release'" 'application release snapshot is immutable'
expect_db_failure "DELETE FROM app_releases WHERE id='$source_release'" 'application releases cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_releases CASCADE; ROLLBACK;' 'immutable history cannot be truncated'

request PUT "$API/apps/$app_id/secrets/LD_PRELOAD" "$user_token" "$(jq -cn --arg value "blocked-$marker" '{value:$value}')"
[[ "$RESPONSE_STATUS" == 400 && "$(response_error_code)" == secret_not_declared ]] || { echo 'undeclared application Secret was not rejected' >&2; exit 1; }
request PUT "$API/apps/$app_id/secrets/ROTATION_KEY" "$user_token" "$(jq -cn --arg value "rotation-one-$marker" '{value:$value}')"
[[ "$RESPONSE_STATUS" == 201 && "$(response_value .version)" == 2 ]] || { echo 'first application Secret rotation was not appended' >&2; exit 1; }
request PUT "$API/apps/$app_id/secrets/ROTATION_KEY" "$user_token" "$(jq -cn --arg value "rotation-two-$marker" '{value:$value}')"
[[ "$RESPONSE_STATUS" == 201 && "$(response_value .version)" == 3 ]] || { echo 'second application Secret rotation was not appended' >&2; exit 1; }
secret_id="$(db "SELECT id FROM app_secrets WHERE user_app_id='$app_id' AND key='ROTATION_KEY'")"
expect_db_failure "UPDATE app_secrets SET key='RENAMED_KEY' WHERE id='$secret_id'" 'application Secret identity is immutable'
expect_db_failure "DELETE FROM app_secrets WHERE id='$secret_id'" 'application Secret records cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_secrets CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
secret_version_id="$(db "SELECT v.id FROM app_secret_versions v JOIN app_secrets s ON s.id=v.app_secret_id WHERE s.user_app_id='$app_id' AND s.key='ROTATION_KEY' ORDER BY v.version DESC LIMIT 1")"
expect_db_failure "UPDATE app_secret_versions SET encrypted_value=encrypted_value || 'changed' WHERE id='$secret_version_id'" 'application secret versions are immutable'
expect_db_failure "DELETE FROM app_secret_versions WHERE id='$secret_version_id'" 'application secret versions are immutable'
expect_db_failure 'BEGIN; TRUNCATE app_secret_versions CASCADE; ROLLBACK;' 'immutable history cannot be truncated'

container="$(db "SELECT upstream_container FROM app_routes WHERE user_app_id='$app_id'")"
public_path="$(db "SELECT public_path FROM app_routes WHERE user_app_id='$app_id'")"
[[ -n "$container" ]] && docker_object_exists container inspect "$container" || { echo 'runtime container was not created' >&2; exit 1; }
establish_app_access "$app_id" "$user_token"
wait_http "$BASE_URL$public_path" 200
expect_db_failure "UPDATE users SET slug=slug || '-changed' WHERE id='$user_id'" 'user public identity is immutable'
expect_db_failure "UPDATE user_apps SET slug=slug || '-changed' WHERE id='$app_id'" 'user application identity is immutable'
expect_db_failure "INSERT INTO users(email,password_hash,display_name,slug) VALUES('invalid-slug-$marker@example.invalid','invalid','Invalid slug','INVALID_$marker')" 'users_slug_format_check'
expect_db_failure "INSERT INTO user_apps(user_id,product_id,slug,service_slug,status) VALUES('$user_id','$product_id','INVALID_$marker','format-$marker','stopped')" 'user_apps_slug_format_check'
expect_db_failure "INSERT INTO user_apps(user_id,product_id,slug,service_slug,status) VALUES('$user_id','$product_id','format-$marker','INVALID_$marker','stopped')" 'user_apps_service_slug_format_check'
expect_db_failure "INSERT INTO user_apps(user_id,product_id,slug,service_slug,status,last_successful_release_id) VALUES('$user_id','$product_id','foreign-$marker','foreign-$marker','running','$source_release')" 'last successful release must belong to the same application'
expect_db_failure "UPDATE app_routes SET public_path='/apps/hijacked/path' WHERE user_app_id='$app_id'" 'application route public path does not match application identity'
expect_db_failure "UPDATE app_routes SET upstream_host='release-000000000000' WHERE user_app_id='$app_id'" 'application route upstream host does not match release identity'
expect_db_failure "UPDATE app_routes SET upstream_port=upstream_port+1 WHERE user_app_id='$app_id'" 'application route upstream port does not match release snapshot'
expect_db_failure "UPDATE app_routes SET upstream_container='cm-00000000000-$app_id-$source_release' WHERE user_app_id='$app_id'" 'application route container does not match instance and release identity'
compose up -d --no-build --force-recreate --no-deps app-router >/dev/null
wait_http "$BASE_URL$public_path" 200

volume="$(app_volume_name "$runtime_owner" "$app_id" data)"
docker_object_exists volume inspect "$volume" || { echo 'persistent application volume was not created' >&2; exit 1; }
[[ "$(docker volume inspect --format '{{json .Labels}}' "$volume" | jq -r '."cloudmeter.owner" // empty')" == "$runtime_owner" ]] || { echo 'persistent application volume is not owned by this runtime' >&2; exit 1; }
docker exec "$container" sh -c "printf '$marker' > /data/lifecycle-marker"

request POST "$API/apps/$app_id/backups" "$user_token" '{"volumeKey":"data"}'
[[ "$RESPONSE_STATUS" == 202 ]] || { echo "backup request was not accepted: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
backup_id="$(response_value .backupId)"
[[ -n "$backup_id" && "$backup_id" != null ]] || { echo 'backup did not return an id' >&2; exit 1; }
wait_backup "$backup_id"
[[ "$(db "SELECT docker_volume FROM app_backups WHERE id='$backup_id'")" == "$volume" ]] || { echo 'backup did not retain the owner-scoped application volume name' >&2; exit 1; }
backup_storage_key="$(db "SELECT storage_key FROM app_backups WHERE id='$backup_id'")"
[[ -n "$backup_storage_key" && "$(db "SELECT coalesce(size_bytes,0) FROM app_backups WHERE id='$backup_id'")" -gt 0 ]] || { echo 'backup did not retain an archive with a positive size' >&2; exit 1; }
docker_object_exists volume inspect "$backup_volume" || { echo 'owner-scoped backup volume was not created' >&2; exit 1; }
backup_volume_owner="$(docker volume inspect --format '{{json .Labels}}' "$backup_volume" | jq -r '."cloudmeter.owner" // empty')"
[[ "$backup_volume_owner" == "$runtime_owner" || (-z "$backup_volume_owner" && "${runtime_owner,,}" == cloudmeter) ]] || { echo 'backup volume is not owned by this runtime' >&2; exit 1; }
docker run --rm -v "$backup_volume:/backup:ro" "$IMAGE" sh -c "test -s /backup/$backup_storage_key" >/dev/null
docker_object_exists container inspect "cm-backup-$runtime_scope_value-$backup_id" && { echo 'backup helper container was not removed' >&2; exit 1; }

docker exec "$container" sh -c "printf 'changed-$marker' > /data/lifecycle-marker"
request POST "$API/apps/$app_id/backups/$backup_id/restore" "$user_token" "$(jq -cn --arg key "control-restore/$marker" '{idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 202 ]] || { echo "restore request was not accepted: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
restore_id="$(response_value .restoreJobId)"
[[ -n "$restore_id" && "$restore_id" != null ]] || { echo 'restore did not return an id' >&2; exit 1; }
wait_restore "$restore_id"
wait_value "SELECT status FROM user_apps WHERE id='$app_id'" running
[[ "$(docker exec "$container" cat /data/lifecycle-marker)" == "$marker" ]] || { echo 'backup restore did not recover the persistent volume contents' >&2; exit 1; }
docker_object_exists container inspect "cm-restore-$runtime_scope_value-$restore_id" && { echo 'restore helper container was not removed' >&2; exit 1; }
expect_db_failure "UPDATE app_backups SET storage_key=storage_key || '.changed' WHERE id='$backup_id'" 'application backup identity is immutable'
expect_db_failure "UPDATE app_backups SET size_bytes=size_bytes+1 WHERE id='$backup_id'" 'completed application backups are immutable'
expect_db_failure "DELETE FROM app_backups WHERE id='$backup_id'" 'immutable history cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_backups CASCADE; ROLLBACK;' 'immutable history cannot be truncated'
expect_db_failure "UPDATE app_restore_jobs SET idempotency_key=idempotency_key || '-changed' WHERE id='$restore_id'" 'application restore job identity is immutable'
expect_db_failure "UPDATE app_restore_jobs SET last_error='changed' WHERE id='$restore_id'" 'completed application restore jobs are immutable'
expect_db_failure "DELETE FROM app_restore_jobs WHERE id='$restore_id'" 'immutable history cannot be deleted'
expect_db_failure 'BEGIN; TRUNCATE app_restore_jobs; ROLLBACK;' 'immutable history cannot be truncated'
expect_db_failure "INSERT INTO app_restore_jobs(backup_id,user_app_id,idempotency_key) VALUES('$backup_id',gen_random_uuid(),'foreign-$marker')" 'application restore job must reference a successful backup of the same application'
deployment_charge_count="$(db "SELECT count(*) FROM usage_events WHERE user_app_id='$app_id' AND usage_code='app.deployment'")"

stop_worker
stop_key="control-stop/$marker"
request POST "$API/apps/$app_id/stop" "$user_token" "$(jq -cn --arg key "$stop_key" '{idempotencyKey:$key}')"
if [[ "$RESPONSE_STATUS" != 202 || "$(response_value .status)" != stopping ]]; then
  echo "stop request was not accepted: $RESPONSE_STATUS code=$(response_error_code) body=$(cat "$RESPONSE_FILE")" >&2
  echo "deployment jobs: $(db "SELECT state FROM deployment_jobs WHERE user_app_id='$app_id' ORDER BY created_at")" >&2
  echo "restore jobs: $(db "SELECT status FROM app_restore_jobs WHERE user_app_id='$app_id' ORDER BY created_at")" >&2
  exit 1
fi
stop_job_id="$(response_value .stopJobId)"
[[ "$(db "SELECT status FROM user_apps WHERE id='$app_id'")" == stopping ]] || { echo 'application did not enter stopping state' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM app_routes WHERE user_app_id='$app_id'")" == 0 ]] || { echo 'public route was not removed atomically' >&2; exit 1; }
wait_http "$BASE_URL$public_path" 404
[[ "$RESPONSE_STATUS" == 404 ]] || { echo 'stopped application route did not return 404 immediately' >&2; exit 1; }
docker_object_exists container inspect "$container" || { echo 'container disappeared before the queued stop task ran' >&2; exit 1; }
request POST "$API/apps/$app_id/stop" "$user_token" "$(jq -cn --arg key "$stop_key" '{idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 200 && "$(response_value .stopJobId)" == "$stop_job_id" ]] || { echo 'stop idempotency replay failed' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM app_stop_jobs WHERE user_app_id='$app_id' AND idempotency_key='$stop_key'")" == 1 ]] || { echo 'stop replay created a duplicate job' >&2; exit 1; }

start_worker
wait_value "SELECT status FROM user_apps WHERE id='$app_id'" stopped
wait_value "SELECT status FROM app_stop_jobs WHERE id='$stop_job_id'" succeeded
docker_object_exists container inspect "$container" && { echo 'stopped application container still exists' >&2; exit 1; }
docker_object_exists volume inspect "$volume" || { echo 'persistent volume was removed while stopping' >&2; exit 1; }

request POST "$API/apps/$app_id/releases" "$user_token" "$(jq -cn --arg version "$version_id" --arg key "control-stopped-update/$marker" '{versionId:$version,idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 409 && "$(response_error_code)" == app_not_running ]] || { echo 'stopped application accepted an update' >&2; exit 1; }
request POST "$API/apps/$app_id/rollback" "$user_token" "$(jq -cn --arg release "$source_release" --arg key "control-stopped-rollback/$marker" '{releaseId:$release,idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 409 && "$(response_error_code)" == app_not_running ]] || { echo 'stopped application accepted a rollback' >&2; exit 1; }

start_key="control-start/$marker"
request POST "$API/apps/$app_id/start" "$user_token" "$(jq -cn --arg key "$start_key" '{idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 202 ]] || { echo "start request was not accepted: $RESPONSE_STATUS $(response_error_code)" >&2; exit 1; }
start_job_id="$(response_value .jobId)"
[[ "$(response_value .releaseId)" != "$source_release" ]] || { echo 'start did not create a new immutable release' >&2; exit 1; }
wait_job "$start_job_id"
wait_value "SELECT status FROM user_apps WHERE id='$app_id'" running
[[ "$(db "SELECT operation || '|' || source_release_id::text FROM deployment_jobs WHERE id='$start_job_id'")" == "start|$source_release" ]] || { echo 'start job metadata is incorrect' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM app_releases WHERE user_app_id='$app_id'")" == 2 ]] || { echo 'start did not append exactly one release' >&2; exit 1; }
[[ "$(db "SELECT count(*) FROM usage_events WHERE user_app_id='$app_id' AND usage_code='app.deployment'")" == "$deployment_charge_count" ]] || { echo 'start incorrectly generated a deployment fee event' >&2; exit 1; }
new_container="$(db "SELECT upstream_container FROM app_routes WHERE user_app_id='$app_id'")"
[[ -n "$new_container" && "$new_container" != "$container" ]] && docker_object_exists container inspect "$new_container" || { echo 'start did not create a new runtime container' >&2; exit 1; }
[[ "$(docker exec "$new_container" cat /data/lifecycle-marker)" == "$marker" ]] || { echo 'persistent volume contents were not preserved across stop and start' >&2; exit 1; }
wait_http "$BASE_URL$public_path" 200
request POST "$API/apps/$app_id/start" "$user_token" "$(jq -cn --arg key "$start_key" '{idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 200 && "$(response_value .jobId)" == "$start_job_id" ]] || { echo 'start idempotency replay failed' >&2; exit 1; }

request POST "$API/apps/$app_id/stop" "$user_token" "$(jq -cn --arg key "control-final-stop/$marker" '{idempotencyKey:$key}')"
[[ "$RESPONSE_STATUS" == 202 ]] || { echo 'final stop request failed' >&2; exit 1; }
wait_value "SELECT status FROM user_apps WHERE id='$app_id'" stopped
db_exec "UPDATE user_apps SET status='suspended',suspension_reason='billing_insufficient' WHERE id='$app_id'"
for operation in start update rollback; do
  case "$operation" in
    start) request POST "$API/apps/$app_id/start" "$user_token" "$(jq -cn --arg key "control-blocked-start/$marker" '{idempotencyKey:$key}')" ;;
    update) request POST "$API/apps/$app_id/releases" "$user_token" "$(jq -cn --arg version "$version_id" --arg key "control-blocked-update/$marker" '{versionId:$version,idempotencyKey:$key}')" ;;
    rollback) request POST "$API/apps/$app_id/rollback" "$user_token" "$(jq -cn --arg release "$source_release" --arg key "control-blocked-rollback/$marker" '{releaseId:$release,idempotencyKey:$key}')" ;;
  esac
  [[ "$RESPONSE_STATUS" == 409 && "$(response_error_code)" == app_suspended ]] || { echo "platform-suspended application accepted $operation" >&2; exit 1; }
done

echo 'Application backup/restore history integrity, immutable Release and Secret history, owner-scoped storage, helper cleanup, Router recreate, stop/start route removal, idempotency, persistent volume retention, lifecycle guards and no-charge restart verification passed'
