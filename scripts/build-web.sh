#!/bin/bash
# build-web.sh — Web asset builder for Docker
#
# Builds the shared TypeScript UI (shared/web/) into:
#   - Docker:  deploy/docker/web-build/web.ts → deploy/docker/web/app.js
#
# Also syncs index.html and styles.css to the Docker target.
# Run this script whenever shared/web/ TypeScript sources change.

set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHARED="$ROOT/shared/web"
DOCKER_OUT="$ROOT/deploy/docker/web"
WEB_BUILD="$ROOT/deploy/docker/web-build"

echo "=== Building web assets ==="

mkdir -p "$DOCKER_OUT"
if [[ ! -d "$WEB_BUILD/node_modules" ]]; then
  npm ci --omit=dev --prefix "$WEB_BUILD"
fi

# 1. Bundle Docker web (fetch adapter)
echo "[1/2] Bundling Docker app.js from deploy/docker/web-build/web.ts ..."
NODE_PATH="$WEB_BUILD/node_modules" npx --yes esbuild@0.25.12 "$WEB_BUILD/web.ts" \
  --bundle --format=iife --target=es2022 --minify \
  --outfile="$DOCKER_OUT/app.js"
echo "      → $DOCKER_OUT/app.js ($(wc -c < "$DOCKER_OUT/app.js") bytes)"

# 2. Sync index.html and styles.css to the Docker target
echo "[2/2] Syncing index.html + styles.css ..."
cp "$SHARED/index.html" "$DOCKER_OUT/index.html"
cp "$SHARED/styles.css" "$DOCKER_OUT/styles.css"
cp "$SHARED/snet-logo.svg" "$DOCKER_OUT/snet-logo.svg"
echo "      → Docker in sync"

echo ""
echo "=== Done ==="
echo "Docker:  $DOCKER_OUT/"