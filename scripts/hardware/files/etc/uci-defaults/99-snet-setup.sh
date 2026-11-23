#!/bin/sh
# First-boot setup for SNET OpenWrt hardware.
set -eu

mkdir -p /etc/snet
chmod 700 /etc/snet

sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
printf '%s\n' 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-snet.conf

# Preset snet.lan -> 127.0.0.1 in dnsmasq so http://snet.lan resolves to the
# device itself on first boot.
if [ ! -f /etc/dnsmasq.conf ] || ! grep -q '^address=/snet\.lan/' /etc/dnsmasq.conf 2>/dev/null; then
	printf '\n# 让 snet.lan 永远指向本机\naddress=/snet.lan/127.0.0.1\n' >> /etc/dnsmasq.conf
fi

if command -v uci >/dev/null 2>&1; then
	if ! uci -q show dhcp | grep -q "name='snet.lan'"; then
		sec="$(uci add dhcp domain)"
		uci set "dhcp.${sec}.name=snet.lan"
		uci set "dhcp.${sec}.ip=127.0.0.1"
		uci commit dhcp
	fi
	if uci -q get nginx._lan >/dev/null 2>&1; then
		uci set nginx._lan.server_name='_ snet.lan _lan'
		if uci -q get nginx._redirect2ssl >/dev/null 2>&1; then
			uci set nginx._redirect2ssl.server_name='_ snet.lan _redirect2ssl'
		fi
		uci commit nginx
	fi
fi

/etc/init.d/dnsmasq reload >/dev/null 2>&1 || /etc/init.d/dnsmasq restart >/dev/null 2>&1 || true
/etc/init.d/nginx reload >/dev/null 2>&1 || /etc/init.d/nginx restart >/dev/null 2>&1 || true

if command -v nft >/dev/null 2>&1 && nft list table inet fw4 >/dev/null 2>&1; then
	nft list chain inet fw4 forward >/dev/null 2>&1 || exit 0
	nft add rule inet fw4 forward iifname "tun*" accept 2>/dev/null || true
	nft add rule inet fw4 forward oifname "tun*" accept 2>/dev/null || true
elif command -v iptables >/dev/null 2>&1; then
	iptables -C FORWARD -i tun+ -j ACCEPT 2>/dev/null || \
		iptables -A FORWARD -i tun+ -j ACCEPT 2>/dev/null || true
	iptables -C FORWARD -o tun+ -j ACCEPT 2>/dev/null || \
		iptables -A FORWARD -o tun+ -j ACCEPT 2>/dev/null || true
else
	logger -t snet "no nftables fw4 or iptables found; configure forwarding manually"
fi

exit 0
