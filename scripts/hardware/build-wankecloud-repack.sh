#!/usr/bin/env bash
# Repack a known-good WankeCloud (S805) Amlogic burn image with a fresh SNET overlay.
#
# This is intentionally a repack pipeline, not a full OpenWrt source build:
# official OpenWrt/ImmortalWrt no longer publish an S805 Image Builder. The
# pinned base image carries the board DDR/U-Boot/boot/resource partitions; only
# the ext4 rootfs is modified with snetd/snetctl/Web UI.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HW_DIR="$ROOT/scripts/hardware"
OUT="$ROOT/dist/hardware/wankecloud"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"

BASE="${SNET_WANKECLOUD_BASE:-https://github.com/S-shidi/Snet/releases/download/v1.2.2/Snet-v1.2.2-wankecloud-s805.burn.img.xz}"
BASE_SHA256="${SNET_WANKECLOUD_BASE_SHA256:-084f92fdb0a57cd6983be7bfc9c4711a6b6ed8656cb876f3d98a0fdb1f77843e}"
AMLIMG_COMMIT="${SNET_AMLIMG_COMMIT:-5ede4ed30b27c10187af5258f7e2f61f9ad71e1e}"
AMLIMG="${SNET_AMLIMG:-}"
CACHE="${SNET_WANKECLOUD_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/snet-wankecloud}"
WORK="${SNET_WANKECLOUD_WORK:-$HW_DIR/.work/wankecloud-repack}"
SKIP_XZ="${SNET_WANKECLOUD_SKIP_XZ:-0}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
for c in go python3 curl tar npm npx debugfs e2fsck sha256sum; do need "$c"; done

echo "==> building SNET armv7 overlay"
rm -rf "$WORK"
mkdir -p "$WORK/overlay/usr/bin" "$WORK/overlay/www/snet" \
  "$WORK/overlay/etc/init.d" "$WORK/overlay/etc/uci-defaults" "$WORK/overlay/etc/config" "$CACHE" "$OUT"
LDFLAGS="-s -w -X github.com/S-shidi/snet/shared/buildinfo.Version=${VERSION} -X github.com/S-shidi/snet/shared/buildinfo.Commit=wankecloud-repack -X github.com/S-shidi/snet/shared/buildinfo.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
(
  cd "$ROOT"
  GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o "$WORK/overlay/usr/bin/snetd" ./cmd/client/snetd
  GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o "$WORK/overlay/usr/bin/snetctl" ./cmd/client/snetctl
)
if [[ ! -d "$ROOT/deploy/docker/web-build/node_modules" ]]; then
  npm ci --omit=dev --prefix "$ROOT/deploy/docker/web-build"
fi
NODE_PATH="$ROOT/deploy/docker/web-build/node_modules" npx --yes esbuild@0.25.12 \
  "$ROOT/deploy/docker/web-build/web.ts" --bundle --format=iife --target=es2022 --minify \
  --outfile="$WORK/overlay/www/snet/app.js" >/dev/null
cp "$ROOT/shared/web/index.html" "$ROOT/shared/web/styles.css" "$ROOT/shared/web/snet-logo.svg" "$WORK/overlay/www/snet/"
cp "$HW_DIR/files/etc/init.d/snetd" "$WORK/overlay/etc/init.d/snetd"
cp "$HW_DIR/files/etc/uci-defaults/99-snet-setup.sh" "$WORK/overlay/etc/uci-defaults/99-snet-setup.sh"
cp "$HW_DIR/files/etc/config/uhttpd" "$WORK/overlay/etc/config/uhttpd"
chmod 0755 "$WORK/overlay/usr/bin/snetd" "$WORK/overlay/usr/bin/snetctl" \
  "$WORK/overlay/etc/init.d/snetd" "$WORK/overlay/etc/uci-defaults/99-snet-setup.sh"

echo "==> fetching AmlImg"
if [[ -z "$AMLIMG" ]]; then
  AMLIMG="$WORK/AmlImg"
  src="$CACHE/AmlImg-$AMLIMG_COMMIT.tar.gz"
  if [[ ! -f "$src" ]]; then
    curl -fL --retry 3 -o "$src" "https://codeload.github.com/hzyitc/AmlImg/tar.gz/$AMLIMG_COMMIT"
  fi
  mkdir -p "$WORK/AmlImg-src"
  tar -xzf "$src" -C "$WORK/AmlImg-src" --strip-components=1
  (cd "$WORK/AmlImg-src" && go build -o "$AMLIMG" .)
fi
[[ -x "$AMLIMG" ]] || { echo "AmlImg is not executable: $AMLIMG" >&2; exit 1; }

echo "==> fetching base burn image"
if [[ "$BASE" =~ ^https?:// ]]; then
  base_file="$CACHE/$(basename "${BASE%%\?*}")"
  if [[ ! -f "$base_file" ]]; then
    curl -fL --retry 3 -o "$base_file" "$BASE"
  fi
else
  base_file="$BASE"
fi
[[ -f "$base_file" ]] || { echo "base image not found: $base_file" >&2; exit 1; }
echo "$BASE_SHA256  $base_file" | sha256sum -c -

echo "==> unpacking base image"
python3 - "$base_file" "$WORK/base.img" <<'PY'
import lzma, shutil, sys
with lzma.open(sys.argv[1], "rb") as f, open(sys.argv[2], "wb") as o:
    shutil.copyfileobj(f, o, 1024 * 1024)
PY
"$AMLIMG" unpack "$WORK/base.img" "$WORK/unpack" >/dev/null
python3 "$HW_DIR/sparse.py" unpack "$WORK/unpack/9.rootfs.PARTITION.sparse" "$WORK/rootfs.ext4" >/dev/null

echo "==> injecting SNET overlay"
cat > "$WORK/debugfs.cmds" <<DEBUGFS
rm /usr/bin/snetd
rm /usr/bin/snetctl
rm /www/snet/app.js
rm /www/snet/index.html
rm /www/snet/styles.css
rm /www/snet/snet-logo.svg
rm /etc/init.d/snetd
rm /etc/uci-defaults/99-snet-setup.sh
write $WORK/overlay/usr/bin/snetd /usr/bin/snetd
write $WORK/overlay/usr/bin/snetctl /usr/bin/snetctl
write $WORK/overlay/www/snet/app.js /www/snet/app.js
write $WORK/overlay/www/snet/index.html /www/snet/index.html
write $WORK/overlay/www/snet/styles.css /www/snet/styles.css
write $WORK/overlay/www/snet/snet-logo.svg /www/snet/snet-logo.svg
write $WORK/overlay/etc/init.d/snetd /etc/init.d/snetd
write $WORK/overlay/etc/uci-defaults/99-snet-setup.sh /etc/uci-defaults/99-snet-setup.sh
write $WORK/overlay/etc/config/uhttpd /etc/config/uhttpd
set_inode_field /usr/bin/snetd mode 0100755
set_inode_field /usr/bin/snetctl mode 0100755
set_inode_field /www/snet/app.js mode 0100644
set_inode_field /www/snet/index.html mode 0100644
set_inode_field /www/snet/styles.css mode 0100644
set_inode_field /www/snet/snet-logo.svg mode 0100644
set_inode_field /etc/init.d/snetd mode 0100755
set_inode_field /etc/uci-defaults/99-snet-setup.sh mode 0100755
set_inode_field /etc/config/uhttpd mode 0100644
DEBUGFS
debugfs -w -f "$WORK/debugfs.cmds" "$WORK/rootfs.ext4" >/dev/null
e2fsck -f -y "$WORK/rootfs.ext4" >/dev/null || true

echo "==> repacking"
python3 "$HW_DIR/sparse.py" pack "$WORK/rootfs.ext4" "$WORK/unpack/9.rootfs.PARTITION.sparse" >/dev/null
img="$OUT/Snet-v${VERSION}-wankecloud-s805.burn.img"
"$AMLIMG" pack "$img" "$WORK/unpack" >/dev/null

if [[ "$SKIP_XZ" != "1" ]]; then
  echo "==> compressing"
  python3 - "$img" "$img.xz" <<'PY'
import lzma, shutil, sys
with open(sys.argv[1], "rb") as f, lzma.open(sys.argv[2], "wb", preset=6) as o:
    shutil.copyfileobj(f, o, 1024 * 1024)
PY
fi

cat > "$OUT/README.txt" <<README
SNET WankeCloud (S805) USB burn package

Version: $VERSION
Base: shiyu1314/openwrt-onecloud 25.12 (pinned burn image)
AmlImg: hzyitc/AmlImg $AMLIMG_COMMIT
Network: eth0 DHCP client, hostname=snet; built-in DHCP server disabled.
Root URL: https://<device-ip>/ redirects to https://<device-ip>/snet/

Physical reset:
  short press (<3s)  reboot
  3s..8s             ignored
  long press (>=8s)  SNET factory reset

Flashing: Amlogic USB_Burning_Tool, decompress .img.xz, import .img,
connect double-male USB, click Start, then power on. If burn mode does not
start, use the board's short-circuit/maskrom procedure.
README
(
  cd "$OUT"
  : > SHA256SUMS
  find . -maxdepth 1 -type f \( -name '*.img' -o -name '*.img.xz' -o -name 'README.txt' \) -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
)
echo "OK: $OUT"
ls -lh "$OUT"
