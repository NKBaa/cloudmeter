#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"; cd "$root"; port="${PLATFORM_PORT:-18080}"; api="http://127.0.0.1:$port/api"
compose=(docker compose --env-file .env -f deploy/compose.yaml)
db(){ "${compose[@]}" exec -T postgres psql -q -U cloudmeter -d cloudmeter -Atc "$1"|head -n1|tr -d '\r'; }
session(){ local id="$1" token sid; token="$(openssl rand -hex 32)"; sid="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$id',digest('$token','sha256'),now()+interval '15 minutes') RETURNING id")"; echo "$sid|$token"; }
admin="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' LIMIT 1")"; user="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='user' AND u.status='active' LIMIT 1")"; test -n "$admin" -a -n "$user"
IFS='|' read -r aid at < <(session "$admin"); IFS='|' read -r uid ut < <(session "$user"); cleanup(){ db "UPDATE sessions SET revoked_at=now() WHERE id IN ('$aid','$uid')" >/dev/null; }; trap cleanup EXIT
key="provider-op-$(openssl rand -hex 8)"; order="$(curl -fsS -H "Authorization: Bearer $ut" -H 'Content-Type: application/json' -d "{\"amountCents\":1,\"provider\":\"manual\",\"idempotencyKey\":\"$key\"}" "$api/payments/orders"|jq -r .orderId)"
test "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $ut" -H 'Content-Type: application/json' -d '{}' "$api/admin/payments/orders/$order/query")" = 403
curl -fsS -H "Authorization: Bearer $at" -H 'Content-Type: application/json' -d '{}' "$api/admin/payments/orders/$order/query"|jq -e '.providerStatus=="pending"'>/dev/null
curl -fsS -H "Authorization: Bearer $at" -H 'Content-Type: application/json' -d '{}' "$api/admin/payments/orders/$order/close"|jq -e '.status=="closed"'>/dev/null
curl -fsS -H "Authorization: Bearer $at" -H 'Content-Type: application/json' -d '{}' "$api/admin/payments/orders/$order/close"|jq -e '.status=="closed"'>/dev/null
echo 'Payment provider query, close, idempotent replay and RBAC verification passed'
