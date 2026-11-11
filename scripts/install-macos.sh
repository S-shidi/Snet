#!/bin/sh
set -eu

REPO="S-shidi/Snet"
API="https://api.github.com/repos/${REPO}/releases/latest"

case "$(uname -m)" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64) ARCH="amd64" ;;
  *) echo "不支持的 CPU 架构: $(uname -m)" >&2; exit 1 ;;
esac

echo "正在查找最新 SnetMac 版本…"
ASSET_URL="$(curl -fsSL "$API" | /usr/bin/python3 -c '
import json, sys
arch = sys.argv[1].lower()
data = json.load(sys.stdin)
assets = [a for a in data.get("assets", []) if a.get("name", "").lower().endswith(".zip")]
preferred = [a for a in assets if arch in a["name"].lower()]
pick = (preferred or assets)
if not pick:
    sys.exit("没有找到 SnetMac .zip 发布包")
print(pick[0]["browser_download_url"])
' "$ARCH")"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/snetmac-install.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

echo "正在下载 $ASSET_URL"
curl -fL --retry 3 --progress-bar -o "$TMP/SnetMac.zip" "$ASSET_URL"

mkdir -p "$TMP/unpack"
ditto -x -k "$TMP/SnetMac.zip" "$TMP/unpack"
APP="$(find "$TMP/unpack" -maxdepth 2 -name 'SnetMac.app' -type d | head -n 1)"
if [ -z "$APP" ]; then
  echo "发布包中没有找到 SnetMac.app" >&2
  exit 1
fi

ARCHS="$(lipo -archs "$APP/Contents/MacOS/SnetMac" 2>/dev/null || true)"
case "$ARCH" in
  amd64) LIPO_ARCH="x86_64" ;;
  *) LIPO_ARCH="$ARCH" ;;
esac
case " $ARCHS " in
  *" $LIPO_ARCH "*) ;;
  *) echo "该安装包不包含 $ARCH 架构（包含: ${ARCHS:-未知}）" >&2; exit 1 ;;
esac

echo "正在安装到 /Applications…"
xattr -cr "$APP"
if ! rm -rf /Applications/SnetMac.app 2>/dev/null || ! cp -R "$APP" /Applications/SnetMac.app 2>/dev/null; then
  echo "需要管理员权限才能写入 /Applications。"
  osascript -e "do shell script \"rm -rf '/Applications/SnetMac.app' && cp -R '$APP' '/Applications/SnetMac.app'\" with administrator privileges"
fi

xattr -cr /Applications/SnetMac.app
chmod +x /Applications/SnetMac.app/Contents/MacOS/SnetMac 2>/dev/null || true
open /Applications/SnetMac.app

echo "安装成功！您可以关闭此终端窗口了。"
exit 0
