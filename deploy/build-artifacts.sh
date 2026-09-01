#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

VERSION=${1:-$(git rev-parse --short HEAD)}
OUT_DIR="$ROOT/artifacts/cloudmeter-$VERSION"
COMPOSE=(docker compose --env-file .env -f deploy/compose.yaml)

mkdir -p "$OUT_DIR"

echo "Building CloudMeter images..."
"${COMPOSE[@]}" build api worker app-router egress-proxy web gateway

echo "Pulling runtime base images for the offline bundle..."
docker pull postgres:17-alpine
docker pull redis:7.4-alpine
docker pull migrate/migrate:v4.18.1
docker pull alpine:3.21
docker pull "nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"
docker tag "nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10" nginx:1.27-alpine

IMAGES=(
  cloudmeter-api:latest
  cloudmeter-worker:latest
  cloudmeter-app-router:latest
  cloudmeter-egress-proxy:latest
  cloudmeter-web:latest
  cloudmeter-gateway:latest
  postgres:17-alpine
  redis:7.4-alpine
  migrate/migrate:v4.18.1
  alpine:3.21
  nginx:1.27-alpine
)

echo "Creating source and image bundles..."
git archive --format=tar.gz --prefix=cloudmeter/ -o "$OUT_DIR/cloudmeter-source-$VERSION.tar.gz" HEAD
docker save "${IMAGES[@]}" -o "$OUT_DIR/cloudmeter-images-$VERSION.tar"

cp configs/.env.example "$OUT_DIR/.env.example"
cp docs/docker-deployment.md "$OUT_DIR/DEPLOYMENT.md"

(cd "$OUT_DIR" && sha256sum cloudmeter-source-"$VERSION".tar.gz cloudmeter-images-"$VERSION".tar > SHA256SUMS)

echo "Artifacts created in $OUT_DIR"
ls -lh "$OUT_DIR"
