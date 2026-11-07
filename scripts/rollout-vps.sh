#!/bin/bash
# Upload the new server-only build to the Hostodo VPS, set admin credentials, restart.
# The VPS is server-only: do not install or upload snetd/snetctl here.
# Usage: SNET_ADMIN_USER=admin SNET_ADMIN_PASSWORD='...' [SNET_ADMIN_TOKEN='...'] [SNET_REQUIRE_DEVICE_AUTH=1] \
#        [SNET_RELAY_V2_ENDPOINT=snet.uizhi.eu.org:8092 SNET_RELAY_SECRET='...' [SNET_RELAY_SECRET_OLD='...']] \
#        [SNET_RELAY_V2_ENDPOINTS=snet-a:8092,snet-b:8092] \
#        scripts/rollout-vps.sh [HOST]
# Requires SSH key access to root@66.187.6.46 (scp + ssh, no password prompt).
# SNET_ADMIN_TOKEN preserves the static bearer-token channel across the env
# rewrite (omit it and the line is dropped from /etc/snet-server.env).
# SNET_REQUIRE_DEVICE_AUTH=1 also enables the enrollment gate on the server
# (only devices that bound an admin-generated authorization code may join).
# Setting SNET_RELAY_V2_ENDPOINT or SNET_RELAY_V2_ENDPOINTS installs/updates
# the standalone snet-relay service and writes both env files with the same
# secret. SNET_RELAY_V2_ENDPOINTS takes precedence and advertises a
# comma-separated failover list; all relays must share SNET_RELAY_SECRET.
# Omit both to leave Relay v2 out of the server env (the old
# /etc/snet-relay.env is left alone).
set -e
HOST="${1:-root@66.187.6.46}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RELAY_V2_ENDPOINTS="${SNET_RELAY_V2_ENDPOINTS:-$SNET_RELAY_V2_ENDPOINT}"
VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
STAMP="$(date -u +%Y%m%d_%H%M%S)"
BACKUP="/usr/local/snet/backups/$VERSION-$STAMP"

# Prefer dist/ artifacts from scripts/build-release.sh; fall back to the
# legacy build/linux-amd64 layout used by older local builds.
DIST="${SNET_DIST:-$ROOT/dist}"
if [ -x "$DIST/snet-server-$VERSION-linux-amd64" ]; then
    SRC_SERVER="$DIST/snet-server-$VERSION-linux-amd64"
    SRC_RELAY="$DIST/snet-relay-$VERSION-linux-amd64"
else
    BIN="${SNET_BIN:-$ROOT/build/linux-amd64}"
    SRC_SERVER="$BIN/server"
    SRC_RELAY="$BIN/relay"
fi

if [ ! -x "$SRC_SERVER" ]; then
    echo "missing linux server binary for $VERSION — run: ./scripts/build-release.sh" >&2
    echo "looked for: $SRC_SERVER" >&2
    exit 2
fi
if [ -n "$RELAY_V2_ENDPOINTS" ]; then
    if [ -z "$SNET_RELAY_SECRET" ]; then
        echo "SNET_RELAY_V2_ENDPOINT(S) requires SNET_RELAY_SECRET" >&2
        exit 2
    fi
    if [ ! -x "$SRC_RELAY" ]; then
        echo "missing linux relay binary for $VERSION — run: ./scripts/build-release.sh" >&2
        echo "looked for: $SRC_RELAY" >&2
        exit 2
    fi
fi
if [ -z "$SNET_ADMIN_USER" ] || [ -z "$SNET_ADMIN_PASSWORD" ]; then
    echo "need SNET_ADMIN_USER and SNET_ADMIN_PASSWORD exported" >&2
    exit 2
fi

deploy_ok=0
restore_on_error() {
    if [ "$deploy_ok" -ne 1 ]; then
        echo "!! deployment failed; restoring previous binaries/db" >&2
        ssh "$HOST" "systemctl stop snet-server snet-relay || true
            cp -a '$BACKUP/server' /usr/local/snet/bin/server 2>/dev/null || true
            cp -a '$BACKUP/relay' /usr/local/snet/bin/relay 2>/dev/null || true
            cp -a '$BACKUP/snet.db' /var/lib/snet/snet.db 2>/dev/null || true
            cp -a '$BACKUP/snet-server.env' /etc/snet-server.env 2>/dev/null || true
            cp -a '$BACKUP/snet-relay.env' /etc/snet-relay.env 2>/dev/null || true
            systemctl start snet-relay 2>/dev/null || true
            systemctl start snet-server || true" >/dev/null 2>&1 || true
    fi
}
trap restore_on_error EXIT

echo "== stop + backup ($VERSION) =="
ssh "$HOST" "systemctl stop snet-server snet-relay || true; mkdir -p '$BACKUP'; \
    cp -a /usr/local/snet/bin/server '$BACKUP/server' 2>/dev/null || true; \
    cp -a /usr/local/snet/bin/relay '$BACKUP/relay' 2>/dev/null || true; \
    cp -a /etc/snet-server.env '$BACKUP/snet-server.env' 2>/dev/null || true; \
    cp -a /etc/snet-relay.env '$BACKUP/snet-relay.env' 2>/dev/null || true; \
    if [ -f /var/lib/snet/snet.db ]; then cp -a /var/lib/snet/snet.db '$BACKUP/snet.db'; fi"

echo "== scp server binary =="
# Upload to a temp name then rename: the running server binary cannot be
# overwritten in place, but mv (rename) atomically replaces it; the systemd
# unit execs the new inode on the next restart.
scp "$SRC_SERVER" "$HOST:/usr/local/snet/bin/.upload-server"
ssh "$HOST" "mv -f /usr/local/snet/bin/.upload-server /usr/local/snet/bin/server && chmod 755 /usr/local/snet/bin/server"
if [ -n "$RELAY_V2_ENDPOINTS" ]; then
    echo "== scp relay binary =="
    scp "$SRC_RELAY" "$HOST:/usr/local/snet/bin/.upload-relay"
    ssh "$HOST" "mv -f /usr/local/snet/bin/.upload-relay /usr/local/snet/bin/relay && chmod 755 /usr/local/snet/bin/relay"
fi

echo "== write /etc/snet-server.env =="
ENV_LINES="SNET_ADMIN_USER=%s\nSNET_ADMIN_PASSWORD=%s\n"
ENV_ARGS="'$SNET_ADMIN_USER' '$SNET_ADMIN_PASSWORD'"
if [ -n "$SNET_ADMIN_TOKEN" ]; then
    ENV_LINES="${ENV_LINES}SNET_ADMIN_TOKEN=%s\n"
    ENV_ARGS="${ENV_ARGS} '$SNET_ADMIN_TOKEN'"
fi
if [ -n "$SNET_REQUIRE_DEVICE_AUTH" ]; then
    ENV_LINES="${ENV_LINES}SNET_REQUIRE_DEVICE_AUTH=%s\n"
    ENV_ARGS="${ENV_ARGS} '$SNET_REQUIRE_DEVICE_AUTH'"
fi
if [ -n "$RELAY_V2_ENDPOINTS" ]; then
    ENV_LINES="${ENV_LINES}SNET_RELAY_V2_ENDPOINTS=%s\nSNET_RELAY_SECRET=%s\nSNET_RELAY_SECRET_OLD=%s\n"
    ENV_ARGS="${ENV_ARGS} '$RELAY_V2_ENDPOINTS' '$SNET_RELAY_SECRET' '${SNET_RELAY_SECRET_OLD:-}'"
fi
ssh "$HOST" "umask 077; printf '$ENV_LINES' $ENV_ARGS > /etc/snet-server.env"

echo "== install systemd unit + restart =="
scp "$ROOT/deploy/snet-server.service" "$HOST:/etc/systemd/system/snet-server.service"
if [ -n "$RELAY_V2_ENDPOINTS" ]; then
    echo "== write /etc/snet-relay.env + install relay unit =="
    ssh "$HOST" "umask 077; printf 'SNET_RELAY_SECRET=%s\nSNET_RELAY_SECRET_OLD=%s\n' '$SNET_RELAY_SECRET' '${SNET_RELAY_SECRET_OLD:-}' > /etc/snet-relay.env"
    scp "$ROOT/deploy/snet-relay.service" "$HOST:/etc/systemd/system/snet-relay.service"
    ssh "$HOST" "systemctl daemon-reload && systemctl enable snet-relay && systemctl restart snet-relay"
fi
ssh "$HOST" "systemctl daemon-reload && systemctl restart snet-server && { systemctl start snet-relay 2>/dev/null || true; }"

echo "== verify version + health =="
SERVER_VERSION="$(ssh "$HOST" "/usr/local/snet/bin/server -version" | awk '{print \$1}')"
if [ "$SERVER_VERSION" != "$VERSION" ]; then
    echo "version mismatch: server=$SERVER_VERSION want=$VERSION" >&2
    exit 1
fi
HEALTH="$(ssh "$HOST" "curl -sk https://127.0.0.1:8090/healthz")"
printf 'healthz: %s\n' "$HEALTH"
case "$HEALTH" in
    *"\"serverVersion\":\"$VERSION\""*) ;;
    *) echo "healthz serverVersion mismatch (want $VERSION)" >&2; exit 1 ;;
esac
ssh "$HOST" "systemctl status snet-server --no-pager | head -6"

if [ -n "$RELAY_V2_ENDPOINTS" ]; then
    echo "== verify relay =="
    RELAY_VERSION="$(ssh "$HOST" "/usr/local/snet/bin/relay -version" | awk '{print \$1}')"
    if [ "$RELAY_VERSION" != "$VERSION" ]; then
        echo "version mismatch: relay=$RELAY_VERSION want=$VERSION" >&2
        exit 1
    fi
    ssh "$HOST" "curl -fsS http://127.0.0.1:8094/healthz"
    ssh "$HOST" "systemctl status snet-relay --no-pager | head -6"
fi

echo "== verify zombie TTL + relay =="
ssh "$HOST" "journalctl -u snet-server --since '-1min' --no-pager | grep -E 'zombie|relay|listen' | head -5 || true"
deploy_ok=1
echo "== done =="
echo "backup: $HOST:$BACKUP"
echo "rollback: ssh $HOST 'systemctl stop snet-server snet-relay; cp -a $BACKUP/server /usr/local/snet/bin/server; cp -a $BACKUP/relay /usr/local/snet/bin/relay 2>/dev/null || true; cp -a $BACKUP/snet.db /var/lib/snet/snet.db; cp -a $BACKUP/snet-server.env /etc/snet-server.env 2>/dev/null || true; cp -a $BACKUP/snet-relay.env /etc/snet-relay.env 2>/dev/null || true; systemctl start snet-relay 2>/dev/null || true; systemctl start snet-server'"
