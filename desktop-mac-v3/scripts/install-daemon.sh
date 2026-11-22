#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "install-daemon.sh must run as root (use the app's administrator prompt)" >&2
  exit 1
fi

APP_BUNDLE="${1:?app bundle path required}"
USER_HOME="${2:-}"
SNETD_SRC="$APP_BUNDLE/Contents/Resources/snetd"
SNETD_DST="/usr/local/bin/snetd"
DATA_DIR="/usr/local/etc/snet"
PLIST="/Library/LaunchDaemons/com.snet.daemon.plist"
LABEL="com.snet.daemon"

if [ ! -x "$SNETD_SRC" ]; then
  echo "应用包损坏，缺少 snetd 核心" >&2
  exit 1
fi

install -d -m 0755 -o root -g wheel "$DATA_DIR"
install -m 0755 -o root -g wheel "$SNETD_SRC" "$SNETD_DST"

# Remove the old per-user config directory, which root cannot reliably use on
# newer macOS/SIP setups. Keep the device identity only in the new data dir.
if [ -n "$USER_HOME" ] && [ -d "$USER_HOME/Library/Application Support/virtual-net" ]; then
  rm -rf "$USER_HOME/Library/Application Support/virtual-net"
fi

cat > "$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$SNETD_DST</string>
    <string>-config</string>
    <string>$DATA_DIR/config.json</string>
    <string>-device-id-file</string>
    <string>$DATA_DIR/device.id</string>
    <string>-ctl</string>
    <string>127.0.0.1:19432</string>
  </array>
  <key>StandardOutPath</key>
  <string>$DATA_DIR/snetd.log</string>
  <key>StandardErrorPath</key>
  <string>$DATA_DIR/snetd.log</string>
</dict>
</plist>
PLIST_EOF

chown root:wheel "$PLIST"
chmod 0644 "$PLIST"

launchctl bootout "system/$LABEL" 2>/dev/null || launchctl bootout system "$PLIST" 2>/dev/null || true
launchctl unload "$PLIST" 2>/dev/null || true
pkill -f "$SNETD_DST" 2>/dev/null || true
if ! launchctl load -w "$PLIST"; then
  launchctl bootstrap system "$PLIST"
  launchctl enable "system/$LABEL" 2>/dev/null || true
fi
launchctl kickstart -k "system/$LABEL" 2>/dev/null || true

echo "installed $LABEL"
