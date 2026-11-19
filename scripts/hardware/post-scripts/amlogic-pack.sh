#!/usr/bin/env bash
# Amlogic (WankeCloud / S805) burn-image post-processing skeleton.
#
# OpenWrt Image Builder normally emits a sysupgrade image. Amlogic USB Burning
# Tool / AmlImg expects a vendor burn package instead. The exact partition
# layout and bootloader files depend on the board; fill in the board-specific
# paths below instead of assuming a generic layout.
#
# Usage: amlogic-pack.sh <openwrt-output.img> [output-dir]
#
# Required vendor inputs (not shipped in this repository):
#   AML_BOOTLOADER   bootloader/u-boot blob accepted by the board
#   AML_DTB          board device tree
#   AML_AMLIMG       path to the AmlImg tool
#   AML_KERNEL       optional kernel partition image from the OpenWrt build
#   AML_ROOTFS       optional rootfs partition image from the OpenWrt build
#
# Typical AmlImg workflow:
#   1. Unpack the vendor burn image or start from the vendor package template.
#   2. Replace kernel/rootfs with the OpenWrt Image Builder output.
#   3. Repack with AmlImg into a .burn.img.
#
# This script deliberately stops with a clear message when that vendor
# material is absent; it never fabricates a bootable Amlogic image.

set -euo pipefail

IMAGE="${1:?usage: amlogic-pack.sh <openwrt-output.img> [output-dir]}"
OUT_DIR="${2:-$(dirname "$IMAGE")}"
AML_IMG="${AML_AMLIMG:-}"
AML_BOOT="${AML_BOOTLOADER:-}"
AML_DTB="${AML_DTB:-}"

if [[ ! -f "$IMAGE" ]]; then
  echo "amlogic-pack: input image not found: $IMAGE" >&2
  exit 2
fi

missing=0
for var in AML_AMLIMG AML_BOOTLOADER AML_DTB; do
  if [[ -z "${!var:-}" ]]; then
    echo "amlogic-pack: missing required environment variable: $var" >&2
    missing=1
  fi
done
if (( missing )); then
  cat >&2 <<EOF
amlogic-pack: WankeCloud burn-image inputs are incomplete.

Required:
  AML_AMLIMG       path to the AmlImg executable
  AML_BOOTLOADER   board u-boot/bootloader blob (e.g. u-boot.bin)
  AML_DTB          board device tree (e.g. wankecloud.dtb)

Optional partition images (recommended for a reproducible package):
  AML_KERNEL       kernel partition image
  AML_ROOTFS       rootfs partition image

OpenWrt output kept at: $IMAGE
Output directory:       $OUT_DIR
EOF
  exit 3
fi

for file in "$AML_BOOT" "$AML_DTB"; do
  if [[ ! -f "$file" ]]; then
    echo "amlogic-pack: required board file not found: $file" >&2
    exit 3
  fi
done
if [[ ! -x "$AML_IMG" ]]; then
  echo "amlogic-pack: AmlImg is not executable: $AML_IMG" >&2
  exit 3
fi

for file in "${AML_KERNEL:-}" "${AML_ROOTFS:-}"; do
  if [[ -n "$file" && ! -f "$file" ]]; then
    echo "amlogic-pack: optional partition image not found: $file" >&2
    exit 3
  fi
done

mkdir -p "$OUT_DIR"

PKG_DIR="$OUT_DIR/aml-package"
rm -rf "$PKG_DIR"
mkdir -p "$PKG_DIR"
cp -a "$AML_BOOT" "$PKG_DIR/u-boot.bin"
cp -a "$AML_DTB" "$PKG_DIR/board.dtb"
if [[ -n "${AML_KERNEL:-}" ]]; then cp -a "$AML_KERNEL" "$PKG_DIR/kernel.img"; fi
if [[ -n "${AML_ROOTFS:-}" ]]; then cp -a "$AML_ROOTFS" "$PKG_DIR/rootfs.img"; fi
cp -a "$IMAGE" "$PKG_DIR/openwrt-image.img"

cat > "$PKG_DIR/README.txt" <<EOF
WankeCloud AmlImg package staging area
=====================================
bootloader: $AML_BOOT
dtb:        $AML_DTB
AmlImg:     $AML_IMG
openwrt:    $IMAGE

This directory is intentionally staged, not flashed automatically. Use the
AmlImg version and partition template that match the exact WankeCloud board
revision. Replace the pack command below with the vendor command for that
revision; do not reuse a bootloader/DTB from another S805 board.
EOF

# TODO(board): Replace this placeholder with the board-specific AmlImg pack
# command. The exact flags depend on the AmlImg version and partition layout.
# Typical shape (verify against the vendor tool):
#
#   "$AML_IMG" pack \
#     --input  "$PKG_DIR" \
#     --output "$OUT_DIR/${IMAGE##*/%.*}.burn.img"
#
# The package must contain a board-specific bootloader, DTB, kernel/rootfs
# partitions and the matching Amlogic partition template.
echo "amlogic-pack: inputs staged in $PKG_DIR" >&2
echo "amlogic-pack: AmlImg partition template is board-specific; fill in the pack command before flashing." >&2
exit 4
