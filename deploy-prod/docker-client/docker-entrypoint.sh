#!/bin/sh
set -eu

# Seed a stable, deployment-unique device identity before snetd starts.
#
# The mounted data volume (/etc/snet) is the source of truth: an existing
# device.id is never overwritten, so restarts and migrations keep the same
# identity. Only a fresh deployment gets a new random ID, or the value of
# SNET_DEVICE_ID when the operator pins one explicitly.
DATA_DIR="${SNET_DATA_DIR:-/etc/snet}"
ID_FILE="$DATA_DIR/device.id"

mkdir -p "$DATA_DIR"
if [ ! -s "$ID_FILE" ]; then
    if [ -n "${SNET_DEVICE_ID:-}" ]; then
        id="$(printf '%s' "$SNET_DEVICE_ID" | tr -d '[:space:]')"
    else
        id="$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
    fi
    if [ -n "$id" ]; then
        printf '%s\n' "$id" > "$ID_FILE"
        chmod 600 "$ID_FILE" 2>/dev/null || true
        echo "snet-entrypoint: seeded device id ${id}"
    fi
fi

# Best-effort IPv4 forwarding for advertised subnets. Synology and other
# container runtimes often mount /proc/sys read-only, so failure is non-fatal:
# relay connectivity still works, but routed LAN subnets need forwarding.
enable_ip_forward() {
    if command -v sysctl >/dev/null 2>&1 && sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1; then
        return 0
    fi
    printf '1' > /proc/sys/net/ipv4/ip_forward 2>/dev/null
}

if [ -r /proc/sys/net/ipv4/ip_forward ]; then
    current="$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || true)"
    if [ "$current" = "1" ]; then
        echo "snet-entrypoint: net.ipv4.ip_forward already enabled"
    elif enable_ip_forward; then
        echo "snet-entrypoint: enabled net.ipv4.ip_forward"
    else
        # Some runtimes allow remounting /proc/sys rw; most (including Synology)
        # do not. Retry once after the remount, then degrade with a clear hint.
        mount -o remount,rw /proc/sys 2>/dev/null || true
        if enable_ip_forward; then
            echo "snet-entrypoint: enabled net.ipv4.ip_forward after remount"
        else
            echo "snet-entrypoint: WARN cannot enable net.ipv4.ip_forward in this container (read-only /proc/sys)" >&2
            echo "snet-entrypoint: WARN on the host run: sysctl -w net.ipv4.ip_forward=1" >&2
        fi
    fi
fi

exec "$@"
