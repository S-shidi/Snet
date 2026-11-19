#!/bin/sh
# SNET-specific factory reset for ext4-rootfs OpenWrt hardware.
#
# This deliberately keeps the installed SNET binaries and nginx proxy in
# place, but clears device identity, network membership, and the Web password.
# The next boot creates a fresh device identity and starts with eth0 DHCP.

set -eu

log() {
	logger -t snet-factory-reset "$*" 2>/dev/null || true
}

log "starting"

# Stop the daemon before removing its identity/config.
/etc/init.d/snetd stop 2>/dev/null || true

rm -f /etc/snet/config.json
rm -f /etc/snet/device.id

# Remove the optional Docker-style config if a customized image added it.
# The SNET locations file is infrastructure and must stay in place.
rm -f /etc/nginx/conf.d/snet.conf

# Restore the management interface to DHCP.
if command -v uci >/dev/null 2>&1; then
	uci -q set network.lan.proto='dhcp'
	uci -q set network.lan.device='eth0'
	uci -q delete network.lan.ipaddr 2>/dev/null || true
	uci -q delete network.lan.netmask 2>/dev/null || true
	uci -q delete network.lan.gateway 2>/dev/null || true
	uci -q delete network.lan.dns 2>/dev/null || true
	uci -q delete network.lan.ip6assign 2>/dev/null || true
	uci -q set dhcp.lan.ignore='1'
	uci -q commit network
	uci -q commit dhcp
else
	mkdir -p /etc/config
	cat > /etc/config/network <<'EOF'
config interface 'loopback'
	option device 'lo'
	option proto 'static'
	option ipaddr '127.0.0.1'
	option netmask '255.0.0.0'

config globals 'globals'
	option packet_steering '1'

config interface 'lan'
	option device 'eth0'
	option proto 'dhcp'
	option hostname 'snet'
EOF
fi

log "factory reset complete; rebooting"
sync
sleep 1
reboot
