#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
DOCKER_BIN="${DOCKER_BIN:-docker}"
COMPOSE=("$DOCKER_BIN" compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then
  COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME")
fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
if [[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]]; then
  COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE")
fi
if [[ "$DOCKER_BIN" == */* ]]; then
  [[ -x "$DOCKER_BIN" ]] || { echo "Docker CLI is not executable: $DOCKER_BIN" >&2; exit 1; }
else
  command -v "$DOCKER_BIN" >/dev/null || { echo 'docker is required' >&2; exit 1; }
fi
DOCKER_MOUNT_ROOT="$ROOT_DIR"
if [[ "$DOCKER_BIN" == *.exe ]] && command -v wslpath >/dev/null; then
  DOCKER_MOUNT_ROOT="$(wslpath -w "$ROOT_DIR")"
fi
[[ -f .env ]] || { echo '.env is required' >&2; exit 1; }
LATEST_MIGRATION="$(find migrations -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -n 1)"
[[ -n "$LATEST_MIGRATION" ]] || { echo 'no migrations found' >&2; exit 1; }
PORT="${PLATFORM_PORT:-$(awk -F= '$1=="PLATFORM_PORT" {print $2}' .env | tail -n 1)}"
PORT="${PORT%$'\r'}"
PORT="${PORT:-8080}"
BIND_IP="${PLATFORM_BIND_IP:-127.0.0.1}"
GATEWAY_IP="${GATEWAY_BIND_IP:-127.0.0.1}"
wait_for_healthy() {
  local service container_id status
  for service in api app-router egress-proxy web gateway; do
    container_id="$("${COMPOSE[@]}" ps -q "$service")"
    [[ -n "$container_id" ]] || { echo "$service container is not running" >&2; return 1; }
    for _ in {1..30}; do
      status="$("$DOCKER_BIN" inspect --format '{{.State.Health.Status}}' "$container_id" 2>/dev/null || true)"
      [[ "$status" == "healthy" ]] && break
      [[ "$status" == "unhealthy" ]] && { echo "$service is unhealthy" >&2; return 1; }
      sleep 2
    done
    [[ "$status" == "healthy" ]] || { echo "timed out waiting for $service health" >&2; return 1; }
  done
}
echo '[1/7] validating compose configuration'
"${COMPOSE[@]}" config --quiet
echo '[2/7] checking current services and Docker socket isolation'
"${COMPOSE[@]}" ps
WORKER_ID="$("${COMPOSE[@]}" ps -q worker)"
API_ID="$("${COMPOSE[@]}" ps -q api)"
EGRESS_ID="$("${COMPOSE[@]}" ps -q egress-proxy)"
[[ -n "$WORKER_ID" && -n "$API_ID" && -n "$EGRESS_ID" ]] || { echo 'api, worker and egress-proxy containers must be running' >&2; exit 1; }
EXPECTED_PROJECT="${COMPOSE_PROJECT_NAME:-cloudmeter}"
for container_id in "$WORKER_ID" "$API_ID" "$EGRESS_ID"; do
  actual_project="$("$DOCKER_BIN" inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$container_id")"
  [[ "$actual_project" == "$EXPECTED_PROJECT" ]] || { echo "container $container_id belongs to Compose project $actual_project, expected $EXPECTED_PROJECT" >&2; exit 1; }
done
[[ "$("$DOCKER_BIN" inspect --format '{{.State.Health.Status}}' "$EGRESS_ID")" == "healthy" ]] || { echo 'egress-proxy must be healthy' >&2; exit 1; }
if "$DOCKER_BIN" inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$WORKER_ID" | grep -Fxq 'DOCKER_EXECUTOR_ENABLED=true'; then
  "$DOCKER_BIN" inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "$WORKER_ID" | grep -Fxq '/var/run/docker.sock' || { echo 'Docker executor is enabled but worker has no Docker socket' >&2; exit 1; }
fi
if "$DOCKER_BIN" inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "$API_ID" | grep -Fxq '/var/run/docker.sock'; then
  echo 'API must never mount the Docker socket' >&2
  exit 1
fi
curl --fail --silent --show-error "http://${BIND_IP}:${PORT}/api/healthz" >/dev/null
echo '[3/7] Docker socket isolation verified'
echo '[4/7] checking migrations and API contracts'
"${COMPOSE[@]}" run --rm migrate >/dev/null
MIGRATION_STATE="$("${COMPOSE[@]}" exec -T postgres psql -U cloudmeter -d cloudmeter -Atc "SELECT version::text || '|' || CASE WHEN dirty THEN 'dirty' ELSE 'clean' END FROM schema_migrations")"
[[ "$MIGRATION_STATE" == "${LATEST_MIGRATION}|clean" ]] || { echo "migration state is $MIGRATION_STATE, expected ${LATEST_MIGRATION}|clean" >&2; exit 1; }
"${COMPOSE[@]}" exec -T gateway caddy validate --config /etc/cloudmeter/runtime/Caddyfile --adapter caddyfile >/dev/null
"$DOCKER_BIN" run --rm -e GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" -v "$DOCKER_MOUNT_ROOT:/src" -w /src golang:1.23-alpine sh -c '/usr/local/go/bin/go test ./internal/httpapi -run TestOpenAPICoversRegisteredRoutes -count=1' >/dev/null
echo '[5/7] validating OpenAPI schema'
"$DOCKER_BIN" run --rm -e npm_config_registry="${NPM_REGISTRY:-https://registry.npmmirror.com}" -v "$DOCKER_MOUNT_ROOT:/work" -w /work node:24-alpine npx --yes '@redocly/cli@2.46.1' lint docs/openapi.yaml --config docs/redocly.yaml >/dev/null
echo '[6/7] restarting services without deleting volumes'
"${COMPOSE[@]}" restart api worker egress-proxy app-router web gateway >/dev/null
if ! wait_for_healthy; then
  "${COMPOSE[@]}" ps >&2
  exit 1
fi
curl --fail --silent --show-error "http://${BIND_IP}:${PORT}/api/healthz" >/dev/null
UNKNOWN_HOST_STATUS="$(curl --silent --show-error -o /dev/null -w '%{http_code}' -H 'Host: invalid-host.example.invalid' "http://${GATEWAY_IP}:80/api/healthz")"
[[ "$UNKNOWN_HOST_STATUS" == 421 ]] || { echo "unknown Host returned $UNKNOWN_HOST_STATUS, expected 421" >&2; exit 1; }
echo '[7/7] restart health verified'
"${COMPOSE[@]}" ps
