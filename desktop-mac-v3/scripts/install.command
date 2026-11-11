#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
APP="$SCRIPT_DIR/SnetMac.app"
TARGET="/Applications/SnetMac.app"

if [ ! -d "$APP" ]; then
  echo "未找到 SnetMac.app，请在 DMG 中运行此脚本。" >&2
  exit 1
fi

echo "正在清除隔离属性…"
xattr -cr "$APP"

echo "正在安装到 /Applications…"
if ! rm -rf "$TARGET" 2>/dev/null || ! cp -R "$APP" "$TARGET" 2>/dev/null; then
  echo "需要管理员权限才能写入 /Applications。"
  osascript -e "do shell script \"rm -rf '$TARGET' && cp -R '$APP' '$TARGET'\" with administrator privileges"
fi

xattr -cr "$TARGET"
chmod +x "$TARGET/Contents/MacOS/SnetMac" 2>/dev/null || true
open "$TARGET"

echo "安装成功！您可以关闭此终端窗口了。"
exit 0
