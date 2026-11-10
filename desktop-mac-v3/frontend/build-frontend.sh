#!/bin/sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
DIST="$ROOT/desktop-mac-v3/frontend/dist"
mkdir -p "$DIST"

cd "$ROOT"
cp shared/web/index.html shared/web/styles.css "$DIST"/

if [ ! -d "$ROOT/deploy/docker/web-build/node_modules" ]; then
  (cd "$ROOT/deploy/docker/web-build" && npm ci --omit=dev)
fi

NODE_PATH="$ROOT/deploy/docker/web-build/node_modules" \
  npx --yes esbuild@0.25.12 deploy/docker/web-build/web.ts \
  --bundle --format=iife --target=es2022 --minify \
  --outfile="$DIST/app.js"
