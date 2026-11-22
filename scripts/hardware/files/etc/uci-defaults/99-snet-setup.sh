#!/bin/sh
# First-boot setup for SNET OpenWrt hardware.
set -eu

mkdir -p /etc/snet
chmod 700 /etc/snet

sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
printf '%s\n' 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-snet.conf

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
