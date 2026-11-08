#!/bin/sh
set -eu

TOKEN_FILE="${SNET_CTL_TOKEN_FILE:-/etc/snet/ctl-token}"
if [ ! -r "$TOKEN_FILE" ]; then
    echo "snet-web: missing ctl-token at $TOKEN_FILE" >&2
    exit 1
fi

TOKEN="$(cat "$TOKEN_FILE")"
if [ -z "$TOKEN" ]; then
    echo "snet-web: empty ctl-token at $TOKEN_FILE" >&2
    exit 1
fi

sed "s|__CTL_TOKEN__|$TOKEN|g" \
    /etc/nginx/snet.conf.template > /etc/nginx/conf.d/default.conf

exec /docker-entrypoint.sh "$@"
