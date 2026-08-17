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
API="http://127.0.0.1:$PORT/api"
LATEST_MIGRATION="$(find migrations -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -n 1)"
ADMIN_SESSION=''
USER_SESSION=''

db() { "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'; }
session() {
  local user_id="$1" token id
  token="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')"
  id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$user_id',digest('$token','sha256'),now()+interval '15 minutes') RETURNING id")"
  printf '%s|%s\n' "$id" "$token"
}
assert_db_rejected() {
  local query="$1" description="$2"
  if "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U cloudmeter -d cloudmeter -c "BEGIN; $query; ROLLBACK;" >/dev/null 2>&1; then
    echo "database accepted forbidden audit mutation: $description" >&2
    exit 1
  fi
}
cleanup() {
  [[ -z "$ADMIN_SESSION" || -z "$USER_SESSION" ]] || db "UPDATE sessions SET revoked_at=now() WHERE id IN ('$ADMIN_SESSION','$USER_SESSION') RETURNING id" >/dev/null 2>&1 || true
}
trap cleanup EXIT

MIGRATION_STATE="$(db "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations")"
[[ "$MIGRATION_STATE" == "$LATEST_MIGRATION|clean" ]] || { echo "migration $LATEST_MIGRATION must be applied before audit verification; current state is $MIGRATION_STATE" >&2; exit 1; }
ADMIN_ID="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
USER_ROW="$(db "SELECT u.id::text || chr(9) || u.email FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$ADMIN_ID" && -n "$USER_ROW" ]] || { echo 'an active super administrator and ordinary user are required' >&2; exit 1; }
IFS=$'\t' read -r USER_ID USER_EMAIL <<<"$USER_ROW"
IFS='|' read -r ADMIN_SESSION ADMIN_TOKEN <<<"$(session "$ADMIN_ID")"
IFS='|' read -r USER_SESSION USER_TOKEN <<<"$(session "$USER_ID")"
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "$API/admin/audit-logs")" == 401 ]] || { echo 'unauthenticated audit access did not return 401' >&2; exit 1; }
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $USER_TOKEN" "$API/admin/audit-logs")" == 403 ]] || { echo 'ordinary user audit access did not return 403' >&2; exit 1; }

MARKER="$(openssl rand -hex 16)"
ACTION_PREFIX="verification.audit.$MARKER"
FIRST_ID="$(db "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES('$ADMIN_ID','$USER_ID','$ACTION_PREFIX.first','audit_verification','$MARKER-first','verify-audit/$MARKER',jsonb_build_object('sequence',1,'marker','$MARKER')) RETURNING id")"
SECOND_ID="$(db "INSERT INTO audit_logs(actor_user_id,subject_user_id,action,resource_type,resource_id,request_id,metadata) VALUES('$ADMIN_ID','$USER_ID','$ACTION_PREFIX.second','audit_verification','$MARKER-second','verify-audit/$MARKER',jsonb_build_object('sequence',2,'marker','$MARKER')) RETURNING id")"
FIRST_PAGE="$(curl -fsS -G -H "Authorization: Bearer $ADMIN_TOKEN" --data-urlencode 'limit=1' --data-urlencode "action=$ACTION_PREFIX" --data-urlencode "identity=$USER_EMAIL" "$API/admin/audit-logs")"
[[ "$(jq -r '.logs | length' <<<"$FIRST_PAGE")" == 1 && "$(jq -r '.logs[0].id' <<<"$FIRST_PAGE")" == "$SECOND_ID" ]] || { echo 'audit filter or first cursor page is incorrect' >&2; exit 1; }
NEXT="$(jq -r '.nextBefore' <<<"$FIRST_PAGE")"
[[ "$NEXT" != null && -n "$NEXT" ]] || { echo 'audit first page omitted its cursor' >&2; exit 1; }
SECOND_PAGE="$(curl -fsS -G -H "Authorization: Bearer $ADMIN_TOKEN" --data-urlencode 'limit=1' --data-urlencode "action=$ACTION_PREFIX" --data-urlencode "identity=$USER_EMAIL" --data-urlencode "before=$NEXT" "$API/admin/audit-logs")"
[[ "$(jq -r '.logs | length' <<<"$SECOND_PAGE")" == 1 && "$(jq -r '.logs[0].id' <<<"$SECOND_PAGE")" == "$FIRST_ID" && "$(jq -r '.nextBefore' <<<"$SECOND_PAGE")" == null ]] || { echo 'audit cursor did not return the earlier record exactly once' >&2; exit 1; }

assert_db_rejected "UPDATE audit_logs SET action=action || '.changed' WHERE id=$FIRST_ID" UPDATE
assert_db_rejected "DELETE FROM audit_logs WHERE id=$FIRST_ID" DELETE
assert_db_rejected 'TRUNCATE audit_logs' TRUNCATE
[[ "$(db "SELECT count(*) FROM audit_logs WHERE id IN ($FIRST_ID,$SECOND_ID) AND metadata->>'marker'='$MARKER'")" == 2 ]] || { echo 'audit evidence changed during immutability verification' >&2; exit 1; }

echo "Audit authorization, filters, cursor pagination and append-only database protection passed ($FIRST_ID, $SECOND_ID)"
