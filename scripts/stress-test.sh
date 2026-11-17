#!/usr/bin/env bash
# Multi-node Snet stress harness.
#
# Starts N isolated snet-client containers on a private Docker bridge, creates
# one network through node 1's control API, joins every other node, prints the
# virtual IPs and can run a ping matrix or a delegated-route sync probe.
#
# Usage (run on sdserver):
#   IMAGE=crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13 ./scripts/stress-test.sh all
#   ./scripts/stress-test.sh up
#   ./scripts/stress-test.sh ping
#   ./scripts/stress-test.sh route-sync
#   ./scripts/stress-test.sh down
#
# Environment:
#   NODES=6                 number of client containers (5-10 recommended)
#   IMAGE=...               client image to run
#   SERVER=https://...      coordination server
#   WG_BASE=53000           first WireGuard UDP port
#   CTL_BASE=19500          first control API port
#   ROUTE_CIDR=192.168.250.0/24  CIDR used by the route-sync probe
#   KEEP=1                  keep containers/configs after `down`
set -Eeuo pipefail

NODES="${NODES:-6}"
IMAGE="${IMAGE:-crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13}"
SERVER="${SERVER:-https://snet.uizhi.eu.org:8090}"
PREFIX="${PREFIX:-snet-stress}"
ROOT="${STRESS_DIR:-/tmp/snet-stress}"
NETWORK="${STRESS_NETWORK:-snet-stress-net}"
WG_BASE="${WG_BASE:-53000}"
CTL_BASE="${CTL_BASE:-19500}"
ROUTE_CIDR="${ROUTE_CIDR:-192.168.250.0/24}"
KEEP="${KEEP:-0}"

log()  { printf '[stress] %s\n' "$*" >&2; }
die()  { printf '[stress] ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

ctl() {
  local i="$1"; shift
  local base="$ROOT/node${i}"
  [ -s "$base/ctl-token" ] || die "node$i ctl token missing"
  curl -fsS --max-time 15 \
    -H "X-Ctl-Token: $(cat "$base/ctl-token")" \
    "$@"
}

ctl_json() {
  local i="$1"; shift
  local method="$1"; shift
  local path="$1"; shift
  local body="${1:-}"
  local base="$ROOT/node${i}"
  [ -s "$base/ctl-token" ] || die "node$i ctl token missing"
  if [ -n "$body" ]; then
    curl -fsS --max-time 20 -X "$method" \
      -H "X-Ctl-Token: $(cat "$base/ctl-token")" \
      -H 'Content-Type: application/json' \
      --data "$body" "http://127.0.0.1:$((CTL_BASE + i - 1))$path"
  else
    curl -fsS --max-time 20 -X "$method" \
      -H "X-Ctl-Token: $(cat "$base/ctl-token")" \
      "http://127.0.0.1:$((CTL_BASE + i - 1))$path"
  fi
}

json_field() {
  python3 -c 'import json,sys; d=json.load(sys.stdin); v=d.get(sys.argv[1],""); print(v if not isinstance(v,(dict,list)) else json.dumps(v))' "$1"
}

node_ip() {
  local i="$1"
  ctl_json "$i" GET /ctl/status | python3 -c '
import json,sys
d=json.load(sys.stdin)
nets=d.get("networks") or []
print(nets[0].get("ip","") if nets else "")'
}

node_id() {
  local i="$1"
  ctl_json "$i" GET /ctl/status >/dev/null 2>&1 || true
  # Node ID is not in status; read it from the network member view.
  ctl_json 1 GET "/ctl/netinfo?nid=$(cat "$ROOT/network.id")" | python3 -c '
import json,sys
ip=sys.argv[1]
d=json.load(sys.stdin)
for n in d.get("nodes",[]):
    if n.get("ip")==ip:
        print(n.get("id","")); break
' "$(node_ip "$i")"
}

wait_ctl() {
  local i="$1" base="$ROOT/node${i}" n=0
  while [ ! -s "$base/ctl-token" ] || [ "$(ctl_json "$i" GET /ctl/status 2>/dev/null | wc -c)" -eq 0 ]; do
    n=$((n+1))
    if [ "$n" -gt 90 ]; then
      docker logs "${PREFIX}-node${i}" >&2 || true
      die "node$i did not become ready"
    fi
    sleep 1
  done
}

start_nodes() {
  need docker
  need curl
  need python3
  [ -e /dev/net/tun ] || die "/dev/net/tun is missing; enable TUN on the Docker host"
  docker info >/dev/null 2>&1 || die "Docker is not available to this user"
  docker image inspect "$IMAGE" >/dev/null 2>&1 || die "image not found: $IMAGE (docker pull first)"

  if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
    docker network create --driver bridge "$NETWORK" >/dev/null
  fi
  mkdir -p "$ROOT"

  local i wg ctl_port dir
  for i in $(seq 1 "$NODES"); do
    dir="$ROOT/node${i}"
    wg=$((WG_BASE + i - 1))
    ctl_port=$((CTL_BASE + i - 1))
    mkdir -p "$dir"
    printf 'stress%08d\n' "$i" > "$dir/device.id"
    cat > "$dir/config.json" <<JSON
{
  "serverAddr": "$SERVER",
  "wireguardPort": $wg,
  "networks": {},
  "keyRotationDays": 30,
  "natMapping": false
}
JSON
    docker rm -f "${PREFIX}-node${i}" >/dev/null 2>&1 || true
    docker run -d --name "${PREFIX}-node${i}" \
      --hostname "stress-node${i}" \
      --network "$NETWORK" \
      --cap-add NET_ADMIN \
      --sysctl net.ipv4.ip_forward=1 \
      --device /dev/net/tun \
      -v "$dir:/etc/snet" \
      -p "127.0.0.1:${ctl_port}:${ctl_port}" \
      -p "${wg}:${wg}/udp" \
      --entrypoint /usr/local/bin/snetd \
      "$IMAGE" \
      -config /etc/snet/config.json \
      -ctl "0.0.0.0:${ctl_port}" \
      -device-id-file /etc/snet/device.id >/dev/null
    wait_ctl "$i"
    log "node$i ready (wg=$wg ctl=$ctl_port)"
  done

  log "creating network through node1"
  local created nid code
  created=$(ctl_json 1 POST /ctl/create "{\"name\":\"stress-$(date +%s)\",\"port\":$WG_BASE,\"approvalRequired\":false}")
  nid=$(printf '%s' "$created" | json_field networkId)
  code=$(printf '%s' "$created" | json_field pairingCode)
  [ -n "$nid" ] && [ -n "$code" ] || die "create returned no network/pairing code: $created"
  printf '%s\n' "$nid" > "$ROOT/network.id"
  printf '%s\n' "$code" > "$ROOT/pairing.code"
  log "network=$nid"

  for i in $(seq 2 "$NODES"); do
    local wg=$((WG_BASE + i - 1))
    ctl_json "$i" POST /ctl/join "{\"nid\":\"$nid\",\"code\":\"$code\",\"port\":$wg}" >/dev/null
    log "node$i joined"
  done

  # Wait until every node reports a virtual IP.
  for i in $(seq 1 "$NODES"); do
    local n=0 ip=""
    while [ -z "$ip" ]; do
      n=$((n+1)); [ "$n" -le 60 ] || die "node$i has no virtual IP"
      sleep 1
      ip=$(node_ip "$i" || true)
    done
  done
}

print_status() {
  [ -s "$ROOT/network.id" ] || die "no network yet; run '$0 up' first"
  printf '\n%-8s %-18s %-7s %-7s %s\n' NODE VIRTUAL_IP WG_PORT CTL_PORT STATUS
  local i ip wg ctl_port state
  for i in $(seq 1 "$NODES"); do
    ip=$(node_ip "$i" 2>/dev/null || true)
    wg=$((WG_BASE + i - 1))
    ctl_port=$((CTL_BASE + i - 1))
    state=$(ctl_json "$i" GET /ctl/status 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
n=(d.get("networks") or [{}])[0]
print(n.get("state","?"))' 2>/dev/null || echo down)
    printf '%-8s %-18s %-7s %-7s %s\n' "node$i" "${ip:--}" "$wg" "$ctl_port" "$state"
  done
  printf '\nnetwork: %s\n' "$(cat "$ROOT/network.id")"
}

ping_matrix() {
  [ -s "$ROOT/network.id" ] || die "no network yet; run '$0 up' first"
  local i target out loss avg
  printf '\nPing from node1:\n'
  for i in $(seq 2 "$NODES"); do
    target=$(node_ip "$i" 2>/dev/null || true)
    [ -n "$target" ] || { printf 'node1 -> node%-2s  (no IP)\n' "$i"; continue; }
    out=$(ctl_json 1 POST /ctl/ping "{\"target\":\"$target\"}" 2>/dev/null || true)
    loss=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("loss",100))' 2>/dev/null || echo 100)
    avg=$(printf '%s' "$out" | python3 -c 'import json,sys; print(round(float(json.load(sys.stdin).get("avgRtt",0)),1))' 2>/dev/null || echo 0)
    printf 'node1 -> node%-2s  %-15s loss=%s%% avg=%sms\n' "$i" "$target" "$loss" "$avg"
  done
}

route_sync() {
  [ -s "$ROOT/network.id" ] || die "no network yet; run '$0 up' first"
  local nid; nid=$(cat "$ROOT/network.id")
  local target_ip target_id i
  target_ip=$(node_ip 2)
  target_id=$(node_id 2)
  [ -n "$target_id" ] || die "could not resolve node2 node ID"
  log "enabling relay gateway on node2 ($target_id)"
  ctl_json 2 POST /ctl/relay '{"allowRelay":true,"useRelay":true}' >/dev/null || die "node2 relay setup failed"
  sleep 2
  local err
  err=$(ctl_json 2 GET /ctl/status | python3 -c 'import json,sys; print(json.load(sys.stdin).get("relayError",""))')
  [ -z "$err" ] || die "node2 relay gateway error: $err"

  local routes_json="{\"nid\":\"$nid\",\"routes\":[{\"targetNodeId\":\"$target_id\",\"cidr\":\"$ROUTE_CIDR\"}]}"
  log "delegating $ROUTE_CIDR to node2"
  ctl_json 1 POST /ctl/routes "$routes_json" >/dev/null || die "route delegation failed"
  local start; start=$(date +%s%3N)
  for i in $(seq 1 80); do
    local ready=1
    for j in $(seq 1 "$NODES"); do
      local n
      n=$(ctl_json "$j" GET "/ctl/peers?nid=$nid" 2>/dev/null | python3 -c '
import json,sys
cidr=sys.argv[1]
d=json.load(sys.stdin)
print(sum(1 for r in d.get("routes",[]) if r.get("cidr")==cidr and r.get("status")=="active"))
' "$ROUTE_CIDR" 2>/dev/null || echo 0)
      [ "$n" = "1" ] || ready=0
    done
    if [ "$ready" = "1" ]; then
      local end; end=$(date +%s%3N)
      printf 'route %s active on all %s nodes in %s ms\n' "$ROUTE_CIDR" "$NODES" "$((end-start))"
      return 0
    fi
    sleep 0.5
  done
  die "route did not become active on all nodes within 40s"
}

down_nodes() {
  local i
  for i in $(seq 1 "$NODES"); do
    docker rm -f "${PREFIX}-node${i}" >/dev/null 2>&1 || true
  done
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  if [ "$KEEP" = "1" ]; then
    log "kept configs in $ROOT"
  else
    rm -rf "$ROOT"
  fi
}

usage() {
  cat <<USAGE
Usage: $0 {all|up|status|ping|route-sync|down}

  up         start N nodes, create a network and join all nodes
  status     print virtual IPs and ports
  ping       ping node2..N from node1 through the mesh
  route-sync enable node2 as a gateway, delegate $ROUTE_CIDR and measure
             how long every node takes to see the active route
  all        up + status + ping
  down       remove containers/network (KEEP=1 keeps configs)
USAGE
}

case "${1:-all}" in
  up) start_nodes ;;
  status) print_status ;;
  ping) ping_matrix ;;
  route-sync) route_sync ;;
  down) down_nodes ;;
  all) start_nodes; print_status; ping_matrix ;;
  -h|--help|help) usage ;;
  *) usage; exit 2 ;;
esac
