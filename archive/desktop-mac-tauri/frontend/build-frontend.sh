#!/bin/sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
FRONTEND="$ROOT/desktop-mac/frontend"

cd "$ROOT"
cp shared/web/index.html shared/web/styles.css shared/web/*.ts shared/web/tsconfig.json "$FRONTEND"/

if [ ! -d "$ROOT/deploy/docker/web-build/node_modules" ]; then
  (cd "$ROOT/deploy/docker/web-build" && npm ci --omit=dev)
fi

NODE_PATH="$ROOT/deploy/docker/web-build/node_modules" \
  npx --yes esbuild@0.25.12 deploy/docker/web-build/web.ts \
  --bundle --format=iife --target=es2022 --minify \
  --outfile="$FRONTEND/app.js"
