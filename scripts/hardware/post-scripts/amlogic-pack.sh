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

if [[ ! -f "$IMAGE" ]]; then
  echo "amlogic-pack: input image not found: $IMAGE" >&2
  exit 2
fi

if [[ -z "$AML_IMG" || ! -x "$AML_IMG" ]]; then
  cat >&2 <<EOF
amlogic-pack: vendor AmlImg tool is not configured.

Set AML_AMLIMG to the AmlImg executable and provide the board bootloader/DTB,
then adapt the pack command in this script for the exact WankeCloud layout.

OpenWrt output kept at: $IMAGE
Output directory:       $OUT_DIR
EOF
  exit 3
fi

mkdir -p "$OUT_DIR"

# TODO(board): Replace this placeholder with the board-specific pack command.
# Example shape (verify against the vendor AmlImg version in use):
#
#   "$AML_IMG" pack \
#     --input  "$(dirname "$IMAGE")/aml-package" \
#     --output "$OUT_DIR/${IMAGE##*/%.*}.burn.img"
#
# The package directory must contain the board bootloader, DTB and the
# OpenWrt kernel/rootfs artifacts.
echo "amlogic-pack: AmlImg is configured, but the WankeCloud partition/pack command is not implemented yet." >&2
echo "amlogic-pack: adapt post-scripts/amlogic-pack.sh for the board before flashing." >&2
exit 4
