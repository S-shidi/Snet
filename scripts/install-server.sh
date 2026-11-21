#!/usr/bin/env bash
# Minimal SNET coordinator + relay + admin installer for a fresh Linux host.
# Production guide: https://github.com/S-shidi/Snet/blob/master/deploy-prod/docker-server/README.md
set -euo pipefail

VERSION="${SNET_VERSION:-1.2.1}"
DIR="${SNET_DIR:-/opt/snet}"
ACR="crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Please run as root (or use sudo)." >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required. Install Docker first: https://docs.docker.com/engine/install/" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required (docker compose)." >&2
  exit 1
fi
if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required for secret generation." >&2
  exit 1
fi

PUBLIC_IP="${SNET_PUBLIC_IP:-}"
if [[ -z "${PUBLIC_IP}" ]]; then
  PUBLIC_IP="$(curl -fsS4 --max-time 8 https://api.ipify.org 2>/dev/null || true)"
fi
if [[ -z "${PUBLIC_IP}" ]]; then
  PUBLIC_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi
if [[ -z "${PUBLIC_IP}" ]]; then
  echo "Could not determine public IP; set SNET_PUBLIC_IP explicitly." >&2
  exit 1
fi

DOMAIN="${SNET_DOMAIN:-${PUBLIC_IP}}"
RELAY_ENDPOINT="${DOMAIN}:8092"

TLS_ARGS=""
TLS_MOUNT=""
SCHEME="http"
if [[ -n "${SNET_TLS_CERT:-}" && -n "${SNET_TLS_KEY:-}" ]]; then
  SCHEME="https"
  TLS_ARGS="      - \"-tls-cert\"
      - \"${SNET_TLS_CERT}\"
      - \"-tls-key\"
      - \"${SNET_TLS_KEY}\""
  TLS_MOUNT="      - /etc/letsencrypt:/etc/letsencrypt:ro"
fi
PUBLIC_URL="${SNET_PUBLIC_URL:-${SCHEME}://${DOMAIN}:8090}"

read_env() {
  local key="$1"
  if [[ -f "${DIR}/.env" ]]; then
    sed -n "s/^${key}=//p" "${DIR}/.env" | head -n1
  fi
}

SECRET="${SNET_RELAY_SECRET:-$(read_env SNET_RELAY_SECRET)}"
INTERNAL_TOKEN="${SNET_INTERNAL_ADMIN_TOKEN:-$(read_env SNET_INTERNAL_ADMIN_TOKEN)}"
ADMIN_USER="${SNET_ADMIN_USER:-$(read_env SNET_ADMIN_USER)}"
ADMIN_PASSWORD="${SNET_ADMIN_PASSWORD:-$(read_env SNET_ADMIN_PASSWORD)}"
[[ -n "${SECRET}" ]] || SECRET="$(openssl rand -hex 32)"
[[ -n "${INTERNAL_TOKEN}" ]] || INTERNAL_TOKEN="$(openssl rand -hex 32)"
[[ -n "${ADMIN_USER}" ]] || ADMIN_USER="admin"
[[ -n "${ADMIN_PASSWORD}" ]] || ADMIN_PASSWORD="$(openssl rand -hex 16)"

mkdir -p "${DIR}"
chmod 700 "${DIR}"
cat > "${DIR}/.env" <<ENV
SNET_VERSION=${VERSION}
SNET_RELAY_SECRET=${SECRET}
SNET_RELAY_V2_ENDPOINTS=${RELAY_ENDPOINT}
SNETSERV_PUBLIC_URL=${PUBLIC_URL}
SNET_INTERNAL_ADMIN_TOKEN=${INTERNAL_TOKEN}
SNET_ADMIN_USER=${ADMIN_USER}
SNET_ADMIN_PASSWORD=${ADMIN_PASSWORD}
SNET_HUB_NODE_ID=${SNET_HUB_NODE_ID:-}
SNET_HUB_PUBLIC_KEY=${SNET_HUB_PUBLIC_KEY:-}
SNET_HUB_ENDPOINT=${SNET_HUB_ENDPOINT:-}
ENV
chmod 600 "${DIR}/.env"
umask 077
cat > "${DIR}/admin-credentials.txt" <<CREDS
URL=${SCHEME}://${DOMAIN}:8095/admin
USERNAME=${ADMIN_USER}
PASSWORD=${ADMIN_PASSWORD}
CREDS
chmod 600 "${DIR}/admin-credentials.txt"

cat > "${DIR}/compose.yaml" <<YAML
services:
  relay:
    image: ${ACR}/snet-relay:${VERSION}
    container_name: snet-relay
    restart: unless-stopped
    environment:
      SNET_RELAY_SECRET: \${SNET_RELAY_SECRET}
    command: ["-addr", "0.0.0.0:8092", "-metrics-addr", "127.0.0.1:8094"]
    ports:
      - "8092:8092/udp"
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:8094/healthz >/dev/null"]
      interval: 10s
      timeout: 3s
      retries: 5

  server:
    image: ${ACR}/snet-snetserv:${VERSION}
    container_name: snet-server
    restart: unless-stopped
    depends_on:
      relay:
        condition: service_healthy
    environment:
      SNET_RELAY_SECRET: \${SNET_RELAY_SECRET}
      SNET_RELAY_V2_ENDPOINTS: \${SNET_RELAY_V2_ENDPOINTS}
      SNET_HUB_NODE_ID: \${SNET_HUB_NODE_ID:-}
      SNET_HUB_PUBLIC_KEY: \${SNET_HUB_PUBLIC_KEY:-}
      SNET_HUB_ENDPOINT: \${SNET_HUB_ENDPOINT:-}
      SNET_INTERNAL_ADMIN_TOKEN: \${SNET_INTERNAL_ADMIN_TOKEN}
    command:
      - "-addr"
      - "0.0.0.0:8090"
      - "-probe-addr"
      - "0.0.0.0:8091"
      - "-db"
      - "/data/snet.db"
      - "-public-url"
      - "\${SNETSERV_PUBLIC_URL}"
      - "-internal-addr"
      - "0.0.0.0:8096"
      - "-internal-token"
      - "\${SNET_INTERNAL_ADMIN_TOKEN}"
$(printf '%s\n' "${TLS_ARGS}")
    ports:
      - "8090:8090/tcp"
      - "8091:8091/udp"
      - "127.0.0.1:8096:8096/tcp"
    volumes:
      - snet-data:/data
$(printf '%s\n' "${TLS_MOUNT}")

  admin-server:
    image: ${ACR}/snet-admin-server:${VERSION}
    container_name: snet-admin-server
    restart: unless-stopped
    depends_on:
      - server
    environment:
      SNET_INTERNAL_ADMIN_TOKEN: \${SNET_INTERNAL_ADMIN_TOKEN}
      SNET_ADMIN_USER: \${SNET_ADMIN_USER}
      SNET_ADMIN_PASSWORD: \${SNET_ADMIN_PASSWORD}
    command:
      - "-addr"
      - "0.0.0.0:8095"
      - "-snet-server"
      - "http://snet-server:8096"
$(printf '%s\n' "${TLS_ARGS}")
    ports:
      - "8095:8095/tcp"
$(printf '%s\n' "${TLS_MOUNT}")

volumes:
  snet-data:
YAML
chmod 600 "${DIR}/compose.yaml"

cd "${DIR}"
docker compose pull
docker compose up -d

echo "Waiting for SNET server..."
for _ in $(seq 1 30); do
  if curl -kfsS --max-time 2 "${SCHEME}://127.0.0.1:8090/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
cat <<INFO

SNET ${VERSION} is running.

  Admin console : ${SCHEME}://${DOMAIN}:8095/admin
  Admin login   : ${DIR}/admin-credentials.txt
  Health        : ${SCHEME}://${DOMAIN}:8090/healthz
  Relay         : ${RELAY_ENDPOINT}
  Internal RPC  : http://127.0.0.1:8096/internal/admin/health (loopback only)
  Install dir   : ${DIR}

Next:
  1. Open the admin console and log in with the generated credentials.
  2. Deploy a client: https://github.com/S-shidi/Snet#-quick-start
  3. In the client, set the SnetServ address to ${SCHEME}://${DOMAIN}:8090

For TLS and production hardening:
https://github.com/S-shidi/Snet/blob/master/deploy-prod/docker-server/README.md
INFO
