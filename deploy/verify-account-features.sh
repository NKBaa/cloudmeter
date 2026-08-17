#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"
docker_bin="${DOCKER_BIN:-docker}"
compose=("$docker_bin" compose)
[[ -n "${COMPOSE_PROJECT_NAME:-}" ]] && compose+=(--project-name "$COMPOSE_PROJECT_NAME")
compose+=(--env-file .env -f deploy/compose.yaml)
port="${PLATFORM_PORT:-8080}"
base_url="http://127.0.0.1:${port}/api"

db() {
  "${compose[@]}" exec -T postgres psql -X -v ON_ERROR_STOP=1 -q -U cloudmeter -d cloudmeter -Atc "$1" | head -n 1 | tr -d '\r'
}

new_token() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

assert_api_error() {
  local method="$1" path="$2" expected_status="$3" expected_code="$4" body="$5" token="${6:-}"
  local response_file status actual_code
  response_file="$(mktemp)"
  if [[ -n "$token" ]]; then
    status="$(curl -sS -o "$response_file" -w '%{http_code}' -X "$method" -H 'Content-Type: application/json' -H "Authorization: Bearer $token" --data "$body" "$base_url$path")"
  else
    status="$(curl -sS -o "$response_file" -w '%{http_code}' -X "$method" -H 'Content-Type: application/json' --data "$body" "$base_url$path")"
  fi
  actual_code="$(python3 - "$response_file" <<'PY'
import json, sys
with open(sys.argv[1], encoding='utf-8') as stream:
    print(json.load(stream).get('error', {}).get('code', ''))
PY
)"
  rm -f "$response_file"
  [[ "$status" == "$expected_status" && "$actual_code" == "$expected_code" ]] || {
    echo "$method $path returned $status/$actual_code, expected $expected_status/$expected_code" >&2
    return 1
  }
}

assert_rate_limited() {
  local body="$1" headers response status actual_code retry_after
  headers="$(mktemp)"; response="$(mktemp)"
  status="$(curl -sS -D "$headers" -o "$response" -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data "$body" "$base_url/auth/verification-code")"
  actual_code="$(python3 - "$response" <<'PY'
import json, sys
with open(sys.argv[1], encoding='utf-8') as stream:
    print(json.load(stream).get('error', {}).get('code', ''))
PY
)"
  retry_after="$(awk 'BEGIN{IGNORECASE=1} /^Retry-After:/ {gsub("\r","",$2); print $2; exit}' "$headers")"
  rm -f "$headers" "$response"
  [[ "$status" == 429 && "$actual_code" == verification_rate_limited && "$retry_after" == 60 ]] || {
    echo "verification resend returned $status/$actual_code with Retry-After=$retry_after" >&2
    return 1
  }
}

mail_code() {
  local network="$1" container="$2" email="$3" mailbox
  mailbox="$("$docker_bin" run --rm --network "$network" busybox:1.37 wget -Y off -qO- "http://${container}:8025/api/v2/messages")"
  MAILBOX="$mailbox" EMAIL="$email" python3 - <<'PY'
import json, os, re
for item in json.loads(os.environ['MAILBOX']).get('items', []):
    recipients = item.get('Content', {}).get('Headers', {}).get('To', [])
    if os.environ['EMAIL'] in recipients:
        match = re.search(r'\b([0-9]{6})\b', item.get('Content', {}).get('Body', ''))
        if match:
            print(match.group(1))
            raise SystemExit(0)
raise SystemExit('verification code was not found in MailHog')
PY
}

marker="$(new_token | cut -c1-12)"
smtp_container="cm-test-smtp-$marker"
test_email="account-$marker@sub.example.com"
lockout_email="lockout-$marker@sub.example.com"
test_password="CloudMeter-Account-$marker"
admin_session_id=""
user_session_id=""
user_id=""
announcement_id=""
smtp_started=false
snapshots_loaded=false
auth_snapshot=""
mail_snapshot=""
password_digest=""
admin_token=""

cleanup() {
  set +e
  if [[ "$snapshots_loaded" == true ]]; then
    safe_auth="$(AUTH_SNAPSHOT="$auth_snapshot" python3 - <<'PY'
import json, os
source=json.loads(os.environ['AUTH_SNAPSHOT'])
print(json.dumps({'registrationEnabled':False,'emailVerificationRequired':False,'blockEmailAliases':bool(source['blockEmailAliases']),'emailDomainWhitelist':source['emailDomainWhitelist']}, separators=(',',':')))
PY
)"
    curl -fsS -X PUT -H 'Content-Type: application/json' -H "Authorization: Bearer $admin_token" --data "$safe_auth" "$base_url/admin/settings/auth" >/dev/null
    restore_mail="$(MAIL_SNAPSHOT="$mail_snapshot" python3 - <<'PY'
import json, os
s=json.loads(os.environ['MAIL_SNAPSHOT'])
print(json.dumps({'enabled':bool(s['enabled']),'host':s['host'],'port':int(s['port']),'username':s['username'],'password':'','fromEmail':s['fromEmail'],'fromName':s['fromName'],'tlsMode':s['tlsMode']}, separators=(',',':')))
PY
)"
    curl -fsS -X PUT -H 'Content-Type: application/json' -H "Authorization: Bearer $admin_token" --data "$restore_mail" "$base_url/admin/settings/mail" >/dev/null
    restore_auth="$(AUTH_SNAPSHOT="$auth_snapshot" python3 - <<'PY'
import json, os
s=json.loads(os.environ['AUTH_SNAPSHOT'])
print(json.dumps({'registrationEnabled':bool(s['registrationEnabled']),'emailVerificationRequired':bool(s['emailVerificationRequired']),'blockEmailAliases':bool(s['blockEmailAliases']),'emailDomainWhitelist':s['emailDomainWhitelist']}, separators=(',',':')))
PY
)"
    curl -fsS -X PUT -H 'Content-Type: application/json' -H "Authorization: Bearer $admin_token" --data "$restore_auth" "$base_url/admin/settings/auth" >/dev/null
    restored_digest="$(db "SELECT encode(digest(password,'sha256'),'hex') FROM smtp_settings WHERE singleton")"
    [[ "$restored_digest" == "$password_digest" ]] || echo 'warning: SMTP password ciphertext changed during verification' >&2
  fi
  [[ -n "$announcement_id" ]] && db "DELETE FROM announcements WHERE id='$announcement_id' RETURNING id" >/dev/null
  [[ -n "$user_id" ]] && db "UPDATE users SET status='suspended',updated_at=now() WHERE id='$user_id' RETURNING id" >/dev/null
  db "DELETE FROM email_verification_codes WHERE lower(email) IN (lower('$test_email'),lower('$lockout_email')) RETURNING id" >/dev/null
  session_ids=()
  [[ -n "$admin_session_id" ]] && session_ids+=("'$admin_session_id'")
  [[ -n "$user_session_id" ]] && session_ids+=("'$user_session_id'")
  if ((${#session_ids[@]})); then
    joined="$(IFS=,; echo "${session_ids[*]}")"
    db "UPDATE sessions SET revoked_at=now() WHERE id IN ($joined) RETURNING id" >/dev/null
  fi
  [[ "$smtp_started" == true ]] && "$docker_bin" rm -f "$smtp_container" >/dev/null
}
trap cleanup EXIT

admin_id="$(db "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE r.code='super_admin' AND u.status='active' ORDER BY u.created_at LIMIT 1")"
[[ -n "$admin_id" ]] || { echo 'an active super administrator is required' >&2; exit 1; }
admin_token="$(new_token)"
admin_session_id="$(db "INSERT INTO sessions(user_id,token_hash,expires_at) VALUES('$admin_id',digest('$admin_token','sha256'),now()+interval '20 minutes') RETURNING id")"
auth_header=( -H "Authorization: Bearer $admin_token" )

auth_snapshot="$(curl -fsS "${auth_header[@]}" "$base_url/admin/settings/auth")"
mail_snapshot="$(curl -fsS "${auth_header[@]}" "$base_url/admin/settings/mail")"
password_digest="$(db "SELECT encode(digest(password,'sha256'),'hex') FROM smtp_settings WHERE singleton")"
snapshots_loaded=true

api_id="$("${compose[@]}" ps -q api | tr -d '\r')"
[[ -n "$api_id" ]] || { echo 'API container is not running' >&2; exit 1; }
# Docker expands the Go template, not Bash.
# shellcheck disable=SC2016
data_network="$("$docker_bin" inspect --format '{{range $name, $config := .NetworkSettings.Networks}}{{println $name}}{{end}}' "$api_id" | tr -d '\r' | grep 'data_network$' | head -n 1)"
[[ -n "$data_network" ]] || { echo 'API data network was not found' >&2; exit 1; }
"$docker_bin" run --rm -d --name "$smtp_container" --network "$data_network" mailhog/mailhog:v1.0.1 >/dev/null
smtp_started=true
sleep 2

baseline_auth='{"registrationEnabled":false,"emailVerificationRequired":false,"blockEmailAliases":true,"emailDomainWhitelist":[]}'
disabled_mail='{"enabled":false,"host":"","port":587,"username":"","password":"","fromEmail":"","fromName":"CloudMeter","tlsMode":"starttls"}'
curl -fsS -X PUT -H 'Content-Type: application/json' "${auth_header[@]}" --data "$baseline_auth" "$base_url/admin/settings/auth" >/dev/null
curl -fsS -X PUT -H 'Content-Type: application/json' "${auth_header[@]}" --data "$disabled_mail" "$base_url/admin/settings/mail" >/dev/null

assert_api_error PUT /admin/settings/auth 400 invalid_email_domain_whitelist '{"registrationEnabled":true,"emailVerificationRequired":false,"blockEmailAliases":true,"emailDomainWhitelist":["example..com"]}' "$admin_token"
verification_policy='{"registrationEnabled":true,"emailVerificationRequired":true,"blockEmailAliases":true,"emailDomainWhitelist":["example.com"]}'
assert_api_error PUT /admin/settings/auth 409 smtp_required_for_email_verification "$verification_policy" "$admin_token"
test_mail="{\"enabled\":true,\"host\":\"$smtp_container\",\"port\":1025,\"username\":\"\",\"password\":\"\",\"fromEmail\":\"verify@cloudmeter.local\",\"fromName\":\"CloudMeter Verify\",\"tlsMode\":\"none\"}"
mail_result="$(curl -fsS -X PUT -H 'Content-Type: application/json' "${auth_header[@]}" --data "$test_mail" "$base_url/admin/settings/mail")"
MAIL_RESULT="$mail_result" python3 - <<'PY'
import json, os
assert json.loads(os.environ['MAIL_RESULT'])['ready'] is True
PY
curl -fsS -X PUT -H 'Content-Type: application/json' "${auth_header[@]}" --data "$verification_policy" "$base_url/admin/settings/auth" >/dev/null
assert_api_error PUT /admin/settings/mail 409 email_verification_requires_smtp "$disabled_mail" "$admin_token"

assert_api_error POST /auth/verification-code 403 email_policy_blocked '{"email":"alias+tag@example.com"}'
assert_api_error POST /auth/verification-code 403 email_policy_blocked '{"email":"alias.tag@example.com"}'
assert_api_error POST /auth/verification-code 403 email_policy_blocked '{"email":"outside@example.net"}'
verification_request="{\"email\":\"$test_email\"}"
verification="$(curl -fsS -X POST -H 'Content-Type: application/json' --data "$verification_request" "$base_url/auth/verification-code")"
VERIFICATION="$verification" python3 - <<'PY'
import json, os
value=json.loads(os.environ['VERIFICATION'])
assert value['sent'] is True and value['required'] is True
PY
assert_rate_limited "$verification_request"
code="$(mail_code "$data_network" "$smtp_container" "$test_email")"

lockout_request="{\"email\":\"$lockout_email\"}"
curl -fsS -X POST -H 'Content-Type: application/json' --data "$lockout_request" "$base_url/auth/verification-code" >/dev/null
lockout_code="$(mail_code "$data_network" "$smtp_container" "$lockout_email")"
wrong_code=000000; [[ "$lockout_code" == 000000 ]] && wrong_code=999999
wrong_registration="{\"displayName\":\"Lockout Verify $marker\",\"email\":\"$lockout_email\",\"password\":\"$test_password\",\"code\":\"$wrong_code\"}"
for _ in {1..5}; do
  assert_api_error POST /auth/register 400 invalid_verification_code "$wrong_registration"
done
correct_after_lockout="{\"displayName\":\"Lockout Verify $marker\",\"email\":\"$lockout_email\",\"password\":\"$test_password\",\"code\":\"$lockout_code\"}"
assert_api_error POST /auth/register 400 invalid_verification_code "$correct_after_lockout"
lockout_state="$(db "SELECT attempt_count::text || '|' || (consumed_at IS NOT NULL)::text FROM email_verification_codes WHERE lower(email)=lower('$lockout_email') ORDER BY created_at DESC LIMIT 1")"
[[ "$lockout_state" == '5|true' ]] || { echo "verification lockout state is $lockout_state" >&2; exit 1; }

register_body="{\"displayName\":\"Account Verify $marker\",\"email\":\"$test_email\",\"password\":\"$test_password\",\"code\":\"$code\"}"
registered="$(curl -fsS -X POST -H 'Content-Type: application/json' --data "$register_body" "$base_url/auth/register")"
REGISTERED="$registered" python3 - <<'PY'
import json, os
assert json.loads(os.environ['REGISTERED'])['registered'] is True
PY
login_body="{\"email\":\"$test_email\",\"password\":\"$test_password\"}"
login="$(curl -fsS -X POST -H 'Content-Type: application/json' --data "$login_body" "$base_url/auth/login")"
readarray -t login_values < <(LOGIN="$login" python3 - <<'PY'
import json, os
value=json.loads(os.environ['LOGIN'])
print(value['token']); print(value['user']['id'])
PY
)
user_token="${login_values[0]}"; user_id="${login_values[1]}"
user_session_id="$(db "SELECT id FROM sessions WHERE user_id='$user_id' AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1")"

assert_api_error POST /admin/announcements 400 validation_failed '{"title":"","content":"bad","severity":"invalid","published":true}' "$admin_token"
announcement_body="{\"title\":\"Account feature verify $marker\",\"content\":\"SMTP, registration policy and announcement verification\",\"severity\":\"warning\",\"published\":true}"
announcement="$(curl -fsS -X POST -H 'Content-Type: application/json' "${auth_header[@]}" --data "$announcement_body" "$base_url/admin/announcements")"
announcement_id="$(ANNOUNCEMENT="$announcement" python3 - <<'PY'
import json, os
print(json.loads(os.environ['ANNOUNCEMENT'])['id'])
PY
)"
visible="$(curl -fsS -H "Authorization: Bearer $user_token" "$base_url/announcements")"
VISIBLE="$visible" ANNOUNCEMENT_ID="$announcement_id" python3 - <<'PY'
import json, os
assert os.environ['ANNOUNCEMENT_ID'] in {item['id'] for item in json.loads(os.environ['VISIBLE'])['announcements']}
PY
curl -fsS -X PATCH -H 'Content-Type: application/json' "${auth_header[@]}" --data '{"published":false}' "$base_url/admin/announcements/$announcement_id" >/dev/null
hidden="$(curl -fsS -H "Authorization: Bearer $user_token" "$base_url/announcements")"
HIDDEN="$hidden" ANNOUNCEMENT_ID="$announcement_id" python3 - <<'PY'
import json, os
assert os.environ['ANNOUNCEMENT_ID'] not in {item['id'] for item in json.loads(os.environ['HIDDEN'])['announcements']}
PY

audit_count="$(db "SELECT count(*) FROM audit_logs WHERE actor_user_id='$admin_id' AND action IN ('auth.policy.update','smtp.settings.update','announcement.create','announcement.publish.update') AND created_at>now()-interval '20 minutes'")"
((audit_count >= 6)) || { echo 'account feature audit records are incomplete' >&2; exit 1; }
echo 'SMTP invariant, rate limit, verification lockout, domain and alias policy, registration, login, announcement visibility and audit smoke test passed'
