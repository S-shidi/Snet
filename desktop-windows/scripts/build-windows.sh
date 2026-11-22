#!/bin/sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
APP_DIR="$ROOT/desktop-windows"
BIN="$APP_DIR/bin"
VERSION="$(cat "$ROOT/VERSION")"

cd "$ROOT"
mkdir -p dist "$BIN"

echo "==> snetd.exe"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -buildvcs=false -ldflags="-s -w" \
  -o dist/snetd-windows-amd64.exe ./cmd/client/snetd

echo "==> snetctl.exe"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -buildvcs=false -ldflags="-s -w" \
  -o dist/snetctl-windows-amd64.exe ./cmd/client/snetctl

echo "==> wintun.dll"
"$ROOT/scripts/fetch-wintun.sh" "$ROOT/dist/wintun.dll"

echo "==> frontend"
"$APP_DIR/frontend/build-frontend.sh"

echo "==> Wails GUI"
cd "$APP_DIR"
CGO_ENABLED=0 wails3 build GOOS=windows GOARCH=amd64

cp "$ROOT/dist/snetd-windows-amd64.exe" "$BIN/snetd.exe"
cp "$ROOT/dist/snetctl-windows-amd64.exe" "$BIN/snetctl.exe"
cp "$ROOT/dist/wintun.dll" "$BIN/wintun.dll"

echo "==> NSIS installer"
wails3 package GOOS=windows GOARCH=amd64

SETUP="$BIN/Snet-Setup-v${VERSION}.exe"
if [ ! -f "$SETUP" ]; then
  echo "installer not produced: $SETUP" >&2
  exit 1
fi
cp "$SETUP" "$ROOT/dist/"
SETUPZIP="$ROOT/dist/Snet-v${VERSION}-win-Setup.zip"
rm -f "$SETUPZIP"
(cd "$ROOT/dist" && zip -q -9 "$(basename "$SETUPZIP")" "$(basename "$SETUP")")

ZIP="$ROOT/dist/Snet-v${VERSION}-win-portable.zip"
rm -f "$ZIP"
(cd "$BIN" && zip -q -9 "$ZIP" Snet.exe snetd.exe snetctl.exe wintun.dll)

echo
echo "Build complete:"
ls -lh "$BIN/Snet.exe" "$BIN/snetd.exe" "$BIN/snetctl.exe" "$BIN/wintun.dll" "$ROOT/dist/Snet-Setup-v${VERSION}.exe" "$SETUPZIP" "$ZIP"
