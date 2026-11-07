#!/bin/bash
# Update the local (Mac) snet daemon, migrate v1 config, restart launchd
# daemon. Also unloads the retired local coordination server (com.snet.server)
# if a previous build had installed it. Requires sudo.
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN=/tmp/snet-build

cd "$ROOT" || exit 1
mkdir -p "$BIN"

VERSION=$(tr -d '[:space:]' < "$ROOT/VERSION")
COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X snet/internal/buildinfo.Version=$VERSION -X snet/internal/buildinfo.Commit=$COMMIT -X snet/internal/buildinfo.BuildDate=$BUILD_DATE"

echo "== build $VERSION =="
go build -trimpath -ldflags "$LDFLAGS" -o "$BIN/snetd" ./cmd/client/snetd
go build -trimpath -ldflags "$LDFLAGS" -o "$BIN/snetctl" ./cmd/client/snetctl
"$BIN/snetd" -version

echo "== install to /usr/local/snet/bin =="
sudo install -m 755 "$BIN/snetd" "$BIN/snetctl" /usr/local/snet/bin/

echo "== remove retired local server (if present) =="
if [ -f /Library/LaunchDaemons/com.snet.server.plist ]; then
  sudo launchctl bootout system/com.snet.server 2>/dev/null || true
  sudo rm -f /Library/LaunchDaemons/com.snet.server.plist
  sudo rm -f /usr/local/snet/bin/server
  echo "local server unloaded"
fi

echo "== restart daemon (v1 config migrates to v2 on next start) =="
sudo launchctl bootout system/com.snet.daemon 2>/dev/null || true
sudo launchctl bootstrap system /Library/LaunchDaemons/com.snet.daemon.plist 2>/dev/null || true
sleep 2

echo "== status =="
/usr/local/snet/bin/snetctl version
/usr/local/snet/bin/snetctl status | head -20

echo "== device identity =="
cat /usr/local/snet/device.id 2>/dev/null || echo "(none yet — will be created on first daemon start)"
echo "== done =="
