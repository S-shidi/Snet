#!/usr/bin/env bash
# Read-only prerequisite check for manual snetd deployment.
set -u

pass=0
fail=0
skip=0

row() {
  printf '%-28s %-6s %s\n' "$1" "$2" "$3"
  case "$2" in
    PASS) pass=$((pass + 1)) ;;
    FAIL) fail=$((fail + 1)) ;;
    SKIP) skip=$((skip + 1)) ;;
  esac
}

row "检查项" "结果" "详情"
row "----------------------------" "------" "------"

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) row "architecture" "PASS" "$arch (use linux-amd64)" ;;
  aarch64|arm64) row "architecture" "PASS" "$arch (use linux-arm64)" ;;
  *) row "architecture" "FAIL" "unsupported: $arch" ;;
esac

if [ -c /dev/net/tun ]; then
  row "/dev/net/tun" "PASS" "present"
else
  row "/dev/net/tun" "FAIL" "missing; snetd cannot create TUN"
fi

if command -v capsh >/dev/null 2>&1; then
  if capsh --print 2>/dev/null | grep -qi 'cap_net_admin'; then
    row "CAP_NET_ADMIN" "PASS" "capsh reports capability"
  else
    row "CAP_NET_ADMIN" "FAIL" "not present in current capabilities"
  fi
else
  row "CAP_NET_ADMIN" "SKIP" "capsh not installed; check manually"
fi

if command -v ss >/dev/null 2>&1; then
  ports=$(ss -lntup 2>/dev/null | grep -E ':(19432|52100)\b' || true)
  if [ -z "$ports" ]; then
    row "ports 19432/52100" "PASS" "free"
  else
    row "ports 19432/52100" "FAIL" "in use: $(echo "$ports" | tr '\n' ' ')"
  fi
else
  row "ports 19432/52100" "SKIP" "ss not installed; check manually"
fi

if command -v curl >/dev/null 2>&1; then
  ip=$(curl -fsS -m 5 https://ifconfig.me 2>/dev/null || true)
  if [ -n "$ip" ]; then
    row "public egress IP" "PASS" "$ip"
  else
    row "public egress IP" "FAIL" "curl https://ifconfig.me failed"
  fi
else
  row "public egress IP" "SKIP" "curl not installed"
fi

echo
echo "PASS=$pass FAIL=$fail SKIP=$skip"
[ "$fail" -eq 0 ]
