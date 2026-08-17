#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
compose=(docker compose)
[[ -n "${COMPOSE_PROJECT_NAME:-}" ]] && compose+=(--project-name "$COMPOSE_PROJECT_NAME")
compose+=(--env-file .env -f deploy/compose.yaml)
port="${PLATFORM_PORT:-8080}"
base_url="http://127.0.0.1:${port}/api"
db() { "${compose[@]}" exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'; }
new_token() { od -An -N32 -tx1 /dev/urandom | tr -d ' \n'; }
marker="$(new_token)"
IFS=, read -r first_user second_user <<<"$(db "SELECT string_agg(id::text,',') FROM (SELECT id FROM users WHERE status='active' ORDER BY created_at LIMIT 2) q")"
[[ -n "${first_user:-}" && -n "${second_user:-}" ]] || { echo "two active users are required" >&2; exit 1; }
first_token="$(new_token)"; second_token="$(new_token)"
first_session="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$first_user',digest('$first_token','sha256'),now()+interval '15 minutes') RETURNING id")"
second_session="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$second_user',digest('$second_token','sha256'),now()+interval '15 minutes') RETURNING id")"
first_notification="$(db "INSERT INTO user_notifications(user_id,kind,severity,event_key,title,content) VALUES('$first_user','low_balance','warning','access-first/$marker','Access marker A','test') RETURNING id")"
second_notification="$(db "INSERT INTO user_notifications(user_id,kind,severity,event_key,title,content) VALUES('$second_user','low_balance','warning','access-second/$marker','Access marker B','test') RETURNING id")"
cleanup() { db "UPDATE sessions SET revoked_at=now() WHERE id IN ('$first_session','$second_session'); DELETE FROM user_notifications WHERE event_key IN ('access-first/$marker','access-second/$marker')" >/dev/null || true; }
trap cleanup EXIT
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "$base_url/notifications")" == 401 ]] || { echo "unauthenticated notification request did not return 401" >&2; exit 1; }
list="$(curl -fsS -H "Authorization: Bearer $first_token" "$base_url/notifications")"
LIST="$list" FIRST="$first_notification" SECOND="$second_notification" python3 - <<'PY'
import json, os
ids = {item["id"] for item in json.loads(os.environ["LIST"])["notifications"]}
assert os.environ["FIRST"] in ids and os.environ["SECOND"] not in ids
PY
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -X PATCH -H "Authorization: Bearer $first_token" "$base_url/notifications/$second_notification/read")" == 404 ]] || { echo "cross-account notification update did not return 404" >&2; exit 1; }
curl -fsS -X PATCH -H "Authorization: Bearer $second_token" "$base_url/notifications/$second_notification/read" | grep -q 'readAt'
echo "Notification authentication, list isolation, cross-account denial and owner update smoke test passed"
