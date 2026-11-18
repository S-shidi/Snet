#!/usr/bin/env bash
# YAML-driven OpenWrt firmware builder for SNET hardware.
#
# Usage:
#   scripts/hardware/build-firmware.sh --device WankeCloud
#   scripts/hardware/build-firmware.sh --all
#   scripts/hardware/build-firmware.sh --list
#
# The Image Builder is downloaded from downloads.openwrt.org unless the device
# entry provides imagebuilder_url. A board-specific post-processing script is
# invoked after the image is produced.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HW_DIR="$ROOT/scripts/hardware"
YAML="$HW_DIR/devices.yaml"
FILES_DIR="$HW_DIR/files"
POST_DIR="$HW_DIR/post-scripts"
WORK="$HW_DIR/.work"
DIST="$ROOT/dist/hardware"
WEB_BUILD="$ROOT/deploy/docker/web-build"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown')"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
IB_CACHE="${SNET_IB_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/snet-openwrt}"

DEVICE=""
ALL=0
LIST=0

usage() {
  sed -n '2,13p' "$0" | sed 's/^# //'
}

die() {
  echo "error: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

have_yaml_parser() {
  command -v yq >/dev/null 2>&1 || python3 -c 'import yaml' >/dev/null 2>&1
}

# Emit a tab-separated matrix. The first line is global metadata; subsequent
# lines are devices. yq is preferred; PyYAML keeps minimal build hosts usable.
yaml_rows() {
  local selected="${1:-}"
  if command -v yq >/dev/null 2>&1; then
    SNET_DEVICE="${selected:-__ALL__}" yq -r '
      "GLOBAL|" + (.openwrt_version // "23.05.5") + "|" + (.imagebuilder_base // "https://downloads.openwrt.org"),
      (.devices[] | select(env(SNET_DEVICE) == "__ALL__" or .name == env(SNET_DEVICE)) |
        ["DEVICE", .name, .target, .profile, .arch,
         (.post_script // ""), (.packages // ""), (.imagebuilder_url // ""),
         (.imagebuilder_version // ""), (.extra_packages // ""), (.image_name // "")] | join("|"))
    ' "$YAML"
    return
  fi

  python3 - "$YAML" "$selected" <<'PY'
import sys
try:
    import yaml
except Exception as exc:
    raise SystemExit(f"PyYAML unavailable: {exc}")
path, selected = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as f:
    data = yaml.safe_load(f) or {}
print("GLOBAL|{}|{}".format(data.get("openwrt_version", "23.05.5"),
                             data.get("imagebuilder_base", "https://downloads.openwrt.org")))
for d in data.get("devices", []):
    if selected and d.get("name") != selected:
        continue
    fields = [
        "DEVICE",
        d.get("name", ""), d.get("target", ""), d.get("profile", ""),
        d.get("arch", ""), d.get("post_script", ""), d.get("packages", ""),
        d.get("imagebuilder_url", ""), d.get("imagebuilder_version", ""),
        d.get("extra_packages", ""), d.get("image_name", ""),
    ]
    print("|".join(str(x) for x in fields))
PY
}

load_globals() {
  local rows
  rows="$(yaml_rows "")"
  IFS='|' read -r _ GLOBAL_VERSION GLOBAL_BASE <<< "$(printf '%s\n' "$rows" | head -n1)"
  [[ -n "${GLOBAL_VERSION:-}" ]] || die "devices.yaml: openwrt_version is empty"
  [[ -n "${GLOBAL_BASE:-}" ]] || die "devices.yaml: imagebuilder_base is empty"
}

device_rows() {
  yaml_rows "${1:-}"
}

list_devices() {
  local rows
  rows="$(device_rows "")"
  printf '%-20s %-24s %-28s %s\n' "NAME" "TARGET" "PROFILE" "ARCH"
  printf '%s\n' "$rows" | awk -F '|' '$1 == "DEVICE" { printf "%-20s %-24s %-28s %s\n", $2, $3, $4, $5 }'
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --device)
        [[ $# -ge 2 ]] || die "--device requires a name"
        DEVICE="$2"
        shift 2
        ;;
      --all)
        ALL=1
        shift
        ;;
      --list)
        LIST=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1 (try --help)"
        ;;
    esac
  done
}

check_dependencies() {
  need_cmd go
  need_cmd curl
  need_cmd tar
  need_cmd node
  need_cmd npm
  need_cmd npx
  have_yaml_parser || die "install yq (preferred) or python3-pyyaml"
  [[ -f "$YAML" ]] || die "device matrix not found: $YAML"
  [[ -d "$FILES_DIR" ]] || die "files override directory not found: $FILES_DIR"
}

go_target() {
  case "$1" in
    armv7) printf 'GOOS=linux GOARCH=arm GOARM=7' ;;
    arm64|aarch64) printf 'GOOS=linux GOARCH=arm64' ;;
    amd64|x86_64) printf 'GOOS=linux GOARCH=amd64' ;;
    mips) printf 'GOOS=linux GOARCH=mips GOMIPS=softfloat' ;;
    mipsel) printf 'GOOS=linux GOARCH=mipsle GOMIPS=softfloat' ;;
    *) die "unsupported device arch in devices.yaml: $1" ;;
  esac
}

build_web() {
  local out="$WORK/web"
  mkdir -p "$out"
  if [[ ! -d "$WEB_BUILD/node_modules" ]]; then
    echo "==> installing web build dependencies"
    npm ci --omit=dev --prefix "$WEB_BUILD"
  fi
  echo "==> building SNET Web UI"
  NODE_PATH="$WEB_BUILD/node_modules" npx --yes esbuild@0.25.12 \
    "$WEB_BUILD/web.ts" \
    --bundle --format=iife --target=es2022 --minify \
    --outfile="$out/app.js"
  cp "$ROOT/shared/web/index.html" "$out/index.html"
  cp "$ROOT/shared/web/styles.css" "$out/styles.css"
}

build_binaries() {
  local arch="$1" out="$2" target
  target="$(go_target "$arch")"
  mkdir -p "$out"
  echo "==> cross-compiling snetd/snetctl ($target)"
  (
    cd "$ROOT"
    env CGO_ENABLED=0 $target go build -trimpath \
      -ldflags "-s -w -X snet/internal/buildinfo.Version=$VERSION -X snet/internal/buildinfo.Commit=$COMMIT -X snet/internal/buildinfo.BuildDate=$BUILD_DATE" \
      -o "$out/snetd" ./cmd/client/snetd
    env CGO_ENABLED=0 $target go build -trimpath \
      -ldflags "-s -w -X snet/internal/buildinfo.Version=$VERSION -X snet/internal/buildinfo.Commit=$COMMIT -X snet/internal/buildinfo.BuildDate=$BUILD_DATE" \
      -o "$out/snetctl" ./cmd/client/snetctl
  )
}

imagebuilder_url() {
  local target="$1" version="$2" base="$3" override="$4"
  if [[ -n "$override" ]]; then
    printf '%s' "$override"
    return
  fi
  local slug="${target//\//-}"
  local filename="openwrt-imagebuilder-${version}-${slug}.Linux-x86_64.tar.xz"
  printf '%s/releases/%s/targets/%s/%s' "${base%/}" "$version" "$target" "$filename"
}

fetch_imagebuilder() {
  local url="$1" dest="$2"
  mkdir -p "$(dirname "$dest")"
  if [[ -s "$dest" ]]; then
    echo "==> using cached Image Builder: $dest"
    return 0
  fi
  echo "==> downloading Image Builder: $url"
  if ! curl -fL --retry 3 --connect-timeout 20 --max-time 900 \
      -o "$dest.part" "$url"; then
    rm -f "$dest.part"
    echo "warning: Image Builder download failed: $url" >&2
    return 1
  fi
  mv "$dest.part" "$dest"
}

extract_imagebuilder() {
  local archive="$1" dest="$2"
  rm -rf "$dest"
  mkdir -p "$dest"
  case "$archive" in
    *.tar.zst)
      if tar --zstd -xf "$archive" -C "$dest" >/dev/null 2>&1; then
        :
      elif command -v zstd >/dev/null 2>&1; then
        zstd -dc "$archive" | tar -xf - -C "$dest"
      else
        die "cannot extract .tar.zst without zstd: $archive"
      fi
      ;;
    *)
      if command -v xz >/dev/null 2>&1; then
        tar -xJf "$archive" -C "$dest"
      else
        python3 - "$archive" "$dest" <<'PY'
import sys, tarfile
with tarfile.open(sys.argv[1], "r:xz") as tf:
    tf.extractall(sys.argv[2])
PY
      fi
      ;;
  esac
  find "$dest" -mindepth 1 -maxdepth 2 -type d -name 'openwrt-imagebuilder-*' | head -n 1
}

build_device() {
  local name="$1" target="$2" profile="$3" arch="$4" post_script="$5"
  local packages="$6" override="$7" device_version="$8" extra_packages="$9" image_name="${10}"
  local version="${device_version:-$GLOBAL_VERSION}"
  local slug
  slug="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-')"
  local stage="$WORK/$slug"
  local out="$DIST/$slug"
  local url cache archive ib_root

  echo
  echo "============================================================"
  echo "SNET firmware: $name"
  echo "target=$target profile=$profile arch=$arch openwrt=$version"
  echo "============================================================"

  rm -rf "$stage"
  mkdir -p "$stage/files/usr/bin" "$out"
  cp -a "$FILES_DIR/." "$stage/files/"
  cp -a "$WORK/web/." "$stage/files/www/snet/"
  build_binaries "$arch" "$stage/files/usr/bin"
  chmod 0755 "$stage/files/etc/init.d/snetd" "$stage/files/etc/uci-defaults/99-snet-setup.sh" \
    "$stage/files/usr/bin/snetd" "$stage/files/usr/bin/snetctl"

  url="$(imagebuilder_url "$target" "$version" "$GLOBAL_BASE" "$override")"
  local -a candidates=("$url")
  if [[ "$url" == *.tar.xz ]]; then
    candidates+=("${url%.tar.xz}.tar.zst")
  fi
  archive=""
  local candidate candidate_archive
  for candidate in "${candidates[@]}"; do
    candidate_archive="$IB_CACHE/$version/$(basename "${candidate%%\?*}")"
    if fetch_imagebuilder "$candidate" "$candidate_archive"; then
      archive="$candidate_archive"
      url="$candidate"
      break
    fi
  done
  [[ -n "$archive" ]] || die "Image Builder download failed for $name
    Check target/version or set imagebuilder_url in devices.yaml."
  ib_root="$(extract_imagebuilder "$archive" "$WORK/imagebuilder-$slug")"
  [[ -n "$ib_root" && -f "$ib_root/Makefile" ]] || die "Image Builder archive has no Makefile: $archive"
  command -v make >/dev/null 2>&1 || die "OpenWrt Image Builder requires GNU make"
  command -v patch >/dev/null 2>&1 || die "OpenWrt Image Builder requires GNU patch (install patch or use the project's builder container)"
  mkdir -p "$ib_root/tmp"

  local requested_packages="$packages"
  if [[ -n "$extra_packages" ]]; then
    requested_packages="${requested_packages:+$requested_packages }$extra_packages"
  fi

  echo "==> running Image Builder ($name)"
  (
    cd "$ib_root"
    make image \
      PROFILE="$profile" \
      PACKAGES="$requested_packages" \
      FILES="$stage/files" 2>&1 | tee "$out/image-builder.log"
  ) || die "Image Builder failed for $name (see $out/image-builder.log)"

  local found=0
  while IFS= read -r -d '' f; do
    cp -a "$f" "$out/"
    found=1
  done < <(find "$ib_root/bin/targets" -type f \( -name '*.img' -o -name '*.img.gz' -o -name '*.bin' -o -name '*.tar.gz' \) -print0)
  (( found )) || die "Image Builder produced no image for $name"

  local primary
  primary="$(find "$out" -maxdepth 1 -type f \( -name '*sysupgrade*' -o -name '*.img' -o -name '*.bin' \) | head -n 1)"
  [[ -n "$primary" ]] || die "no primary image found for $name"

  if [[ -n "$image_name" ]]; then
    local ext="${primary##*.}"
    cp -a "$primary" "$out/${image_name}.${ext}"
  fi

  if [[ -n "$post_script" ]]; then
    local post="$POST_DIR/$post_script"
    [[ -f "$post" ]] || die "post-script not found: $post"
    echo "==> post-processing with $post_script"
    bash "$post" "$primary" "$out" || die "post-script failed: $post_script"
  fi

  echo "==> output: $out"
  ls -lh "$out"
  echo "OK: $name"
}

main() {
  parse_args "$@"
  check_dependencies
  load_globals
  if (( LIST )); then
    list_devices
    return
  fi
  [[ -n "$DEVICE" || $ALL -eq 1 ]] || die "select --device NAME or --all (use --list to see devices)"
  mkdir -p "$WORK" "$DIST" "$IB_CACHE"
  build_web

  local rows
  rows="$(device_rows "$DEVICE")"
  local count=0
  while IFS='|' read -r kind name target profile arch post_script packages override device_version extra_packages image_name; do
    [[ "$kind" == "DEVICE" ]] || continue
    count=$((count + 1))
    build_device "$name" "$target" "$profile" "$arch" "$post_script" "$packages" "$override" "$device_version" "$extra_packages" "$image_name"
  done <<< "$rows"
  (( count )) || die "device not found in devices.yaml: ${DEVICE:-<all>}"
}

main "$@"
