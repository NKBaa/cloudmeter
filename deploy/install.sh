#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

command -v docker >/dev/null 2>&1 || { echo "Docker is required." >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "Docker Compose v2 is required." >&2; exit 1; }

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

random_base64() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32 | tr -d '=\n'
  else
    head -c 32 /dev/urandom | base64 | tr -d '=\n'
  fi
}

if [[ ! -f .env ]]; then
  umask 077
  cat > .env <<EOF
PLATFORM_BIND_IP=0.0.0.0
PLATFORM_PORT=${PLATFORM_PORT:-8080}
GATEWAY_ACCESS_MODE=
GATEWAY_BIND_IP=0.0.0.0
GATEWAY_TRUSTED_PROXY_CIDRS=172.16.0.0/12
CADDY_ADMIN_URL=http://gateway:2019
CADDYFILE_PATH=/etc/cloudmeter/runtime/Caddyfile
API_TRUSTED_PROXY_CIDRS=172.16.0.0/12
POSTGRES_PASSWORD=$(random_hex)
REDIS_PASSWORD=$(random_hex)
SESSION_TTL_HOURS=24
SECRETS_ENCRYPTION_KEY=$(random_base64)
DOCKER_EXECUTOR_ENABLED=true
DOCKER_SOCKET=/var/run/docker.sock
DOCKER_SOCKET_PATH=/var/run/docker.sock
ROUTER_INTERNAL_TOKEN=$(random_hex)
EGRESS_INGEST_TOKEN=$(random_hex)
BACKUP_STORAGE_VOLUME=cloudmeter_backup_data
BACKUP_STORAGE_MOUNT_VOLUME=cloudmeter_backup_data
BACKUP_HELPER_IMAGE=nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
EOF
  chmod 600 .env
  echo "Generated .env with random credentials. Back up this file securely."
else
  echo "Using existing .env; stored credentials were not changed."
fi

docker compose --env-file .env -f deploy/compose.yaml config --quiet
docker compose --env-file .env -f deploy/compose.yaml up -d --build

port="$(awk -F= '$1=="PLATFORM_PORT" {print $2}' .env | tail -n 1)"
port="${port:-8080}"
echo
echo "CloudMeter is starting. Open: http://127.0.0.1:${port}/setup"
echo "For a remote server, replace 127.0.0.1 with its IP address."
echo "After setup, configure the server public URL in Infrastructure > Website Settings."
