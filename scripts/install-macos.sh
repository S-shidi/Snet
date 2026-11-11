#!/bin/sh
set -eu

REPO="S-shidi/Snet"
API="https://api.github.com/repos/${REPO}/releases/latest"
MIRRORS="https://ghproxy.net/ https://mirror.ghproxy.com/"

case "$(uname -m)" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64) ARCH="amd64" ;;
  *) echo "不支持的 CPU 架构: $(uname -m)" >&2; exit 1 ;;
esac

# curl inherits HTTPS_PROXY / HTTP_PROXY from the caller. The mirror prefixes
# are tried only when the direct GitHub request fails.
fetch_github() {
  url="$1"
  out="$2"
  for prefix in "" $MIRRORS; do
    candidate="${prefix}${url}"
    if [ -n "$prefix" ]; then
      echo "GitHub 直连失败，尝试镜像: ${prefix}" >&2
    fi
    if curl -fL --retry 3 --retry-delay 2 --connect-timeout 15 --max-time 300 \
      --progress-bar -o "$out" "$candidate"; then
      return 0
    fi
  done
  return 1
}

echo "正在查找最新 SnetMac 版本…"
ASSET_JSON="$(mktemp "${TMPDIR:-/tmp}/snetmac-release.XXXXXX")"
trap 'rm -f "$ASSET_JSON"' EXIT
if ! fetch_github "$API" "$ASSET_JSON" 2>/dev/null; then
  echo "无法访问 GitHub Release API，请检查网络或设置 HTTPS_PROXY。" >&2
  exit 1
fi
ASSET_URL="$(/usr/bin/python3 -c '
import json, sys
arch = sys.argv[1].lower()
data = json.load(sys.stdin)
assets = [a for a in data.get("assets", []) if a.get("name", "").lower().endswith(".zip")]
preferred = [a for a in assets if arch in a["name"].lower()]
pick = (preferred or assets)
if not pick:
    sys.exit("没有找到 SnetMac .zip 发布包")
print(pick[0]["browser_download_url"])
' "$ARCH" < "$ASSET_JSON")"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/snetmac-install.XXXXXX")"
trap 'rm -rf "$TMP" "$ASSET_JSON"' EXIT

# Remove the old service and processes before replacing the App. The new App
# installs/upgrades the LaunchDaemon on its first launch.
PLIST="/Library/LaunchDaemons/com.snet.daemon.plist"
if [ -f "$PLIST" ]; then
  echo "检测到旧版 SNET 服务，正在停止…"
  sudo launchctl bootout system "$PLIST" 2>/dev/null || true
  sudo killall -9 snetd 2>/dev/null || true
  sudo killall -9 SnetMac 2>/dev/null || true
fi

echo "正在下载 $ASSET_URL"
if ! fetch_github "$ASSET_URL" "$TMP/SnetMac.zip"; then
  echo "下载失败，请检查网络或设置 HTTPS_PROXY。" >&2
  exit 1
fi

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

install_app() {
  rm -rf /Applications/SnetMac.app && cp -R "$APP" /Applications/SnetMac.app
}

if [ -w /Applications ] && install_app 2>/dev/null; then
  :
else
  echo "需要管理员权限才能写入 /Applications，正在请求 macOS 授权…"
  osascript -e "do shell script \"rm -rf '/Applications/SnetMac.app' && cp -R '$APP' '/Applications/SnetMac.app'\" with administrator privileges"
fi

xattr -cr /Applications/SnetMac.app
chmod +x /Applications/SnetMac.app/Contents/MacOS/SnetMac 2>/dev/null || true
open /Applications/SnetMac.app

echo "安装成功！您可以关闭此终端窗口了。"
exit 0
