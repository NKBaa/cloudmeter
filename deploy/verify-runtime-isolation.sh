#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
COMPOSE=(docker compose)
if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then COMPOSE+=(--project-name "$COMPOSE_PROJECT_NAME"); fi
COMPOSE+=(--env-file .env -f deploy/compose.yaml)
if [[ -n "${CLOUDMETER_COMPOSE_OVERRIDE:-}" ]]; then COMPOSE+=(-f "$CLOUDMETER_COMPOSE_OVERRIDE"); fi
WORKER_ID="$("${COMPOSE[@]}" ps -q worker)"; ROUTER_ID="$("${COMPOSE[@]}" ps -q app-router)"; PROXY_ID="$("${COMPOSE[@]}" ps -q egress-proxy)"
[[ -n "$WORKER_ID" && -n "$ROUTER_ID" && -n "$PROXY_ID" ]] || { echo 'worker, app-router and egress-proxy must be running' >&2; exit 1; }
RUNTIME_OWNER="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$WORKER_ID" | sed -n 's/^RUNTIME_OWNER=//p' | head -n 1)"
[[ -n "$RUNTIME_OWNER" ]] || { echo 'worker must expose RUNTIME_OWNER for scoped verification' >&2; exit 1; }
[[ "$(docker inspect --format '{{ index .Config.Labels "cloudmeter.owner" }}' "$ROUTER_ID")" == "$RUNTIME_OWNER" ]] || { echo 'app-router owner label does not match worker runtime owner' >&2; exit 1; }
[[ "$(docker inspect --format '{{ index .Config.Labels "cloudmeter.owner" }}' "$PROXY_ID")" == "$RUNTIME_OWNER" ]] || { echo 'egress-proxy owner label does not match worker runtime owner' >&2; exit 1; }
mapfile -t APP_IDS < <(docker ps --filter "label=cloudmeter.owner=$RUNTIME_OWNER" --filter name=cm- -q)
(( ${#APP_IDS[@]} > 0 )) || { echo 'at least one running CloudMeter application container is required' >&2; exit 1; }
declare -a APP_NETWORKS APP_ALIASES APP_IMAGES
for id in "${APP_IDS[@]}"; do
  name="$(docker inspect --format '{{.Name}}' "$id")"
  [[ "$(docker inspect --format '{{.HostConfig.Privileged}}' "$id")" == false ]] || { echo "user container $name is privileged" >&2; exit 1; }
  [[ "$(docker inspect --format '{{.HostConfig.NetworkMode}}' "$id")" != host ]] || { echo "user container $name uses host networking" >&2; exit 1; }
  [[ "$(docker inspect --format '{{.HostConfig.PidMode}}' "$id")" != host ]] || { echo "user container $name uses host PID" >&2; exit 1; }
  docker inspect --format '{{range .HostConfig.SecurityOpt}}{{println .}}{{end}}' "$id" | grep -Fxq 'no-new-privileges:true' || { echo "user container $name is missing no-new-privileges" >&2; exit 1; }
  while IFS= read -r bind; do
    [[ -z "$bind" ]] && continue; [[ "$bind" != *'/var/run/docker.sock'* ]] || { echo "user container $name mounts Docker socket" >&2; exit 1; }
    source_path="${bind%%:*}"; [[ "$source_path" != /* ]] || { echo "user container $name has a host path bind" >&2; exit 1; }
  done < <(docker inspect --format '{{range .HostConfig.Binds}}{{println .}}{{end}}' "$id")
  mapfile -t networks < <(docker inspect --format '{{range $name,$endpoint := .NetworkSettings.Networks}}{{println $name}}{{end}}' "$id" | sed '/^$/d')
  (( ${#networks[@]} == 1 )) && [[ "${networks[0]}" == user_net_* ]] || { echo "user container $name must only join its user network" >&2; exit 1; }
  network="${networks[0]}"; [[ "$(docker network inspect --format '{{.Internal}}' "$network")" == true ]] || { echo "user network $network is not internal" >&2; exit 1; }
  alias="$(docker inspect --format '{{range $name,$endpoint := .NetworkSettings.Networks}}{{range $endpoint.Aliases}}{{println .}}{{end}}{{end}}' "$id" | grep '^release-' | head -n 1)"
  [[ -n "$alias" ]] || { echo "user container $name has no immutable release alias" >&2; exit 1; }
  APP_NETWORKS+=("$network"); APP_ALIASES+=("$alias"); APP_IMAGES+=("$(docker inspect --format '{{.Config.Image}}' "$id")")
done
mapfile -t ACTIVE_NETWORKS < <(printf '%s\n' "${APP_NETWORKS[@]}" | sort -u)
for network in "${ACTIVE_NETWORKS[@]}"; do
  format="{{range \$name,\$endpoint := .NetworkSettings.Networks}}{{if eq \$name \"$network\"}}{{range \$endpoint.Aliases}}{{println .}}{{end}}{{end}}{{end}}"
  docker inspect --format "$format" "$ROUTER_ID" | grep -Fxq cloudmeter-app-router || { echo "router lacks stable alias for $network" >&2; exit 1; }
  docker inspect --format "$format" "$PROXY_ID" | grep -Fxq cloudmeter-egress-proxy || { echo "egress proxy lacks stable alias for $network" >&2; exit 1; }
done
docker run --rm --network "${APP_NETWORKS[0]}" --cap-drop ALL --security-opt no-new-privileges:true "${APP_IMAGES[0]}" wget -q -T 5 -O /dev/null "http://${APP_ALIASES[0]}"
for platform_name in postgres api redis; do if docker run --rm --network "${APP_NETWORKS[0]}" --cap-drop ALL --security-opt no-new-privileges:true "${APP_IMAGES[0]}" getent hosts "$platform_name" >/dev/null 2>&1; then echo "user network can resolve platform service $platform_name" >&2; exit 1; fi; done
RUNTIME_SCOPE="$(printf '%s' "$RUNTIME_OWNER" | sha256sum | cut -c1-10)"
TEMP_NETWORK="user_net_verify_${RUNTIME_SCOPE}_$(date +%s%N)"; trap 'docker network rm "$TEMP_NETWORK" >/dev/null 2>&1 || true' EXIT
docker network create --internal --label cloudmeter.managed=true --label "cloudmeter.owner=$RUNTIME_OWNER" "$TEMP_NETWORK" >/dev/null
if docker run --rm --network "$TEMP_NETWORK" --cap-drop ALL --security-opt no-new-privileges:true "${APP_IMAGES[0]}" getent hosts "${APP_ALIASES[0]}" >/dev/null 2>&1; then echo "cross-user network resolved another user's release alias" >&2; exit 1; fi
docker inspect --format '{{range .Mounts}}{{println .Destination}}{{end}}' "$WORKER_ID" | grep -Fxq /var/run/docker.sock || { echo 'worker does not have Docker socket' >&2; exit 1; }
echo "Runtime isolation smoke test passed for ${#APP_IDS[@]} application container(s) across ${#ACTIVE_NETWORKS[@]} user network(s)"
