#!/bin/sh
set -eu

TOKEN_FILE="${SNET_CTL_TOKEN_FILE:-/etc/snet/ctl-token}"
i=0
while [ ! -s "$TOKEN_FILE" ]; do
    i=$((i + 1))
    if [ "$i" -ge 30 ]; then
        echo "snet-web: missing ctl-token at $TOKEN_FILE" >&2
        exit 1
    fi
    sleep 1
done

TOKEN="$(cat "$TOKEN_FILE")"
sed "s|__CTL_TOKEN__|$TOKEN|g" \
    /etc/nginx/snet.conf.template > /etc/nginx/http.d/default.conf

mkdir -p /run/nginx /var/log/nginx
exec nginx -g "daemon off;"
