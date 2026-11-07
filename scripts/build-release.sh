#!/bin/sh
# Build versioned release binaries into dist/ and write checksums.txt.
# Usage: scripts/build-release.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

VERSION=$(tr -d '[:space:]' < VERSION)
[ -n "$VERSION" ] || { echo "VERSION is empty" >&2; exit 1; }
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || printf 'unknown')
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
DIST=${DIST:-$ROOT/dist}

LDFLAGS="-s -w -X snet/internal/buildinfo.Version=$VERSION -X snet/internal/buildinfo.Commit=$COMMIT -X snet/internal/buildinfo.BuildDate=$BUILD_DATE"

build() {
	os=$1
	arch=$2
	pkg=$3
	name=$4
	ext=${5:-}
	printf 'build %-10s %-7s %s\n' "$os" "$arch" "$name"
	GOOS=$os GOARCH=$arch CGO_ENABLED=0 \
		go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/$name$ext" "$pkg"
	chmod 755 "$DIST/$name$ext"
}

rm -rf "$DIST"
mkdir -p "$DIST"
chmod 755 "$DIST"

# Coordination server: deployed on Linux hosts.
build linux amd64 ./cmd/server "snet-server-$VERSION-linux-amd64"
build linux arm64 ./cmd/server "snet-server-$VERSION-linux-arm64"

# Standalone Relay v2: deployed on relay hosts (may be the same VPS).
build linux amd64 ./cmd/relay "snet-relay-$VERSION-linux-amd64"
build linux arm64 ./cmd/relay "snet-relay-$VERSION-linux-arm64"

# Client daemon and CLI: Linux, macOS, and Windows.
for os in linux darwin; do
	for arch in amd64 arm64; do
		build "$os" "$arch" ./cmd/client/snetd "snetd-$VERSION-$os-$arch"
		build "$os" "$arch" ./cmd/client/snetctl "snetctl-$VERSION-$os-$arch"
	done
done
build windows amd64 ./cmd/client/snetd "snetd-$VERSION-windows-amd64" .exe
build windows amd64 ./cmd/client/snetctl "snetctl-$VERSION-windows-amd64" .exe

: > "$DIST/checksums.txt"
for f in "$DIST"/*; do
	[ -f "$f" ] || continue
	[ "$(basename "$f")" = checksums.txt ] && continue
	base=$(basename "$f")
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$DIST" && sha256sum "$base") >> "$DIST/checksums.txt"
	else
		(cd "$DIST" && shasum -a 256 "$base") >> "$DIST/checksums.txt"
	fi
done
chmod 644 "$DIST/checksums.txt"

printf '\nrelease %s built in %s\n' "$VERSION" "$DIST"
