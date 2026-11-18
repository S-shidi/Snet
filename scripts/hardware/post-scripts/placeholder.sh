#!/usr/bin/env bash
# No-op post-processing hook for targets that already produce a flashable
# OpenWrt image (for example Rockchip .img).
set -euo pipefail

IMAGE="${1:?usage: placeholder.sh <image> [output-dir]}"
OUT_DIR="${2:-$(dirname "$IMAGE")}"
mkdir -p "$OUT_DIR"
echo "post-scripts: no platform transformation required for $(basename "$IMAGE")"
