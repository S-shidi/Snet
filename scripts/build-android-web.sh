#!/usr/bin/env bash
# Build shared/web for the Android WebView and place it in app assets.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/android/app-native/app/src/main/assets"
WEB_BUILD="$ROOT/deploy/docker/web-build"

mkdir -p "$OUT"
npm ci --omit=dev --prefix "$WEB_BUILD"

NODE_PATH="$WEB_BUILD/node_modules" npx --yes esbuild@0.25.12 \
  "$WEB_BUILD/web.ts" \
  --bundle --format=iife --target=es2022 --minify \
  --outfile="$OUT/app.js"

cp "$ROOT/shared/web/index.html" "$OUT/index.html"
cp "$ROOT/shared/web/styles.css" "$OUT/styles.css"
cp "$ROOT/android/app-native/web/android-bridge.js" "$OUT/android-bridge.js"

python3 - "$OUT/index.html" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
html = path.read_text()
needle = '<script src="app.js"></script>'
replacement = '<script src="android-bridge.js"></script>\n<script src="app.js"></script>'
if needle not in html:
    raise SystemExit("index.html: app.js script tag not found")
path.write_text(html.replace(needle, replacement, 1))
PY

echo "Android web assets written to $OUT"
