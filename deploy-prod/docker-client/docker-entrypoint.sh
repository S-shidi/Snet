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

exec "$@"
