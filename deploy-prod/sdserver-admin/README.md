# Admin Server on a SNET member host

The admin console runs on a normal SNET member (for example sdserver) and reaches
the VPS coordination server over the SNET virtual network. The VPS must **not**
publish 8096 to the public internet.

## 1. VPS requirements

- `snetserv` runs with:
  - `-internal-addr 0.0.0.0:8096`
  - `-internal-allow-cidr 10.88.0.0/16`
- Compose binds 8096 to the VPS SNET IP, e.g.
  `10.88.0.1:8096:8096/tcp` (`SNET_INTERNAL_BIND_IP=10.88.0.1`).
- The VPS is a member of the SNET network that the admin host belongs to.
- Firewall allows 8096 only from the SNET CIDR/interface and loopback.

## 2. Admin host

Prerequisites:

- Docker + Compose v2
- The host is an active member of the same SNET network (has a `tun*` interface)
- The VPS SNET IP is reachable, e.g. `ping 10.88.0.1`

Deploy:

```bash
mkdir -p /opt/stacks/snet-admin
cd /opt/stacks/snet-admin
cp deploy-prod/sdserver-admin/compose.yaml .
cp deploy-prod/sdserver-admin/.env.example .env
chmod 600 .env
# Fill in SNET_INTERNAL_ADMIN_TOKEN, SNET_ADMIN_USER, SNET_ADMIN_PASSWORD.
docker compose up -d
```

Verify:

```bash
curl -s -H "X-Internal-Admin-Token: $SNET_INTERNAL_ADMIN_TOKEN" \
  http://10.88.0.1:8096/internal/admin/health
curl -s -o /dev/null -w '%{http_code}\n' http://10.10.11.11:8095/admin
```

The admin console is available on the internal LAN at
`http://10.10.11.11:8095/admin`.

## Notes

- The console is bound to the internal LAN IP, not `0.0.0.0`.
- If the direct WireGuard path to the VPS is one-way in a given topology, force
  the admin path through Relay v2 on the admin host (allow only 8091/8092 UDP to
  the VPS public IP). This keeps large HTTP responses stable.
- `admin-server` and `snetserv` should be built from the same commit; verify with
  `admin-server -version` and `snetserv -version`.
