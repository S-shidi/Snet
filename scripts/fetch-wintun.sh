#!/bin/sh
set -eu

VERSION="0.14.1"
ZIP_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
DLL_SHA256="e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
URL="https://www.wintun.net/builds/wintun-${VERSION}.zip"
OUT="${1:-dist/wintun.dll}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
need curl
need unzip
need sha256sum

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "Downloading wintun ${VERSION}..."
curl -fsSL --retry 3 --max-time 120 -o "$tmp/wintun.zip" "$URL"
echo "$ZIP_SHA256  $tmp/wintun.zip" | sha256sum -c - >/dev/null
unzip -q "$tmp/wintun.zip" "wintun/bin/amd64/wintun.dll" -d "$tmp"
echo "$DLL_SHA256  $tmp/wintun/bin/amd64/wintun.dll" | sha256sum -c - >/dev/null
mkdir -p "$(dirname "$OUT")"
cp "$tmp/wintun/bin/amd64/wintun.dll" "$OUT"
echo "wintun.dll ${VERSION}: $OUT ($DLL_SHA256)"
