# SNET 生产 Docker Compose（VPS：snetserv + relay）

目标：在 VPS 上用 Docker Compose 部署 `snetserv` 和 `relay`，启用 Relay v2。管理台部署在 SNET 成员主机，见 `deploy-prod/sdserver-admin/`。
本目录不提交真实 `.env`；`.env` 在 VPS 上生成并保持 0600。

> 服务端只运行 `cmd/snetserv`。管理台 `cmd/admin-server` 部署在 SNET 成员主机，
> 通过 SNET 虚拟网络访问内部 RPC。公网 API `:8090` 不暴露 `/admin`，VPS 不暴露 8095。

## 1. 前置

- Docker Engine + Docker Compose v2
- 可写目录：`deploy-prod/docker-server/`
- TLS 证书：默认挂载 `/etc/letsencrypt` 到容器只读
  - 默认路径：`/etc/letsencrypt/live/snet.uizhi.eu.org/fullchain.pem` 和 `privkey.pem`
  - 路径不同则在 `.env` 设置 `TLS_CERT_PATH` / `TLS_KEY_PATH`
- 公网 DNS：`snet.uizhi.eu.org` 指向 VPS
- 防火墙：放行 8090/tcp、8091/udp、8092/udp；不要放行 8094。8096 只绑定 SNET 虚拟 IP，并用 iptables 限制来源。
- 8096/tcp 只绑定 `127.0.0.1`，用于本机 curl/排障；跨容器访问走 Compose 网络

`snetserv` 支持直接 TLS：`-tls-cert` / `-tls-key`。Compose 已传入证书，不需要 nginx/caddy 反代。

## 2. 生成 secret 与 .env

在 VPS 的 `deploy-prod/docker-server/`：

```bash
cp .env.example .env
chmod 600 .env
umask 077
openssl rand -hex 32 > /tmp/relay.secret
# 将 /tmp/relay.secret 的内容填入 .env 的 SNET_RELAY_SECRET=
# 不要把 secret 打印到终端历史/日志；填写后删除 /tmp/relay.secret
```

`.env` 示例：

```dotenv
SNET_RELAY_SECRET=<32-byte-hex>
SNET_RELAY_V2_ENDPOINTS=snet.uizhi.eu.org:8092
SNETSERV_PUBLIC_URL=https://snet.uizhi.eu.org:8090
SNET_INTERNAL_ADMIN_TOKEN=<32-byte-hex>
SNET_INTERNAL_BIND_IP=10.88.0.1
```

relay 和 snetserv 必须使用**同一个** `SNET_RELAY_SECRET`；管理台主机上的
`admin-server` 必须使用与 snetserv 相同的 `SNET_INTERNAL_ADMIN_TOKEN`。

## 3. 构建与启动

Dockerfile 使用 **COPY 预编译二进制**（`dist-deploy/linux-${BIN_ARCH}/{snetserv,relay}`），不在 VPS 上运行 Go 构建。`admin-server` 镜像在管理台主机或 CI 单独构建/拉取。默认 `BIN_ARCH=amd64`；`uname -m` 为 `aarch64` 时在 `.env` 设置 `BIN_ARCH=arm64`。

```bash
cd deploy-prod/docker-server
docker compose --env-file .env up -d --build
```

顺序由 Compose 保证：

1. `relay` 先启动并通过 `/healthz` 健康检查；
2. `server`（snetserv）等 `relay` healthy 后启动；
3. `server` 读取 `SNET_RELAY_V2_ENDPOINTS` + `SNET_RELAY_SECRET`，签发 Relay v2 ticket。

查看状态：

```bash
docker compose ps
docker compose logs -f relay server
```

## 4. 端口 / 防火墙

| 端口 | 协议 | 暴露 | 用途 |
|---|---|---|---|
| 8090 | TCP | 公网 | snetserv HTTPS API |
| 8091 | UDP | 公网 | public-IP probe |
| 8092 | UDP | 公网 | Relay v2 |
| 8094 | TCP | **仅容器网络/本机** | relay metrics/healthz，不映射 |
| 8096 | TCP | **仅 SNET 虚拟 IP** | snetserv 内部 Admin RPC，iptables 限制 10.88.0.0/16 + loopback |

8094 只用于 Compose healthcheck 和容器内排障；不要发布到公网。

## 5. 验证

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
curl -fsS -o /dev/null -w '%{http_code}\n' https://snet.uizhi.eu.org:8090/admin   # 404
# 从 SNET 成员主机调用内部 RPC（需要服务令牌）
curl -fsS -H "X-Internal-Admin-Token: $SNET_INTERNAL_ADMIN_TOKEN" http://10.88.0.1:8096/internal/admin/health
docker compose ps
docker compose exec relay wget -qO- http://127.0.0.1:8094/healthz
```

预期：

- `/healthz` 返回 `serverVersion` 和 `status:"ok"`；
- `relay`、`server` 都是 running/healthy；
- 公网 `:8090/admin` 返回 404；公网 8095/8096 不可达；
- server 日志包含 Relay v2 ticket 配置；
- 客户端 `/ctl/status` 能看到 `relayPath`，`/peers` 返回 `RelayV2.Enabled=true` 和 endpoint `snet.uizhi.eu.org:8092`。

## 6. 数据与备份

- snetserv bbolt：volume `snet-data` → 容器 `/data/snet.db`
- 备份：`docker run --rm -v <project>_snet-data:/data -v "$PWD":/backup alpine tar czf /backup/snet-data.tgz -C /data .`
- 证书：`/etc/letsencrypt` 只读挂载；certbot 续期后容器读取新文件，无需重建镜像。

## 7. 注意

- 不要在 `.env` 中提交真实 secret；`.gitignore` 已忽略 `deploy-prod/docker-server/.env`。
- relay 必须先起，secret 必须一致；否则 server 签发的 ticket 无法被 relay 验证。
- Relay v2 使用 MTU 1320（`protocol.DefaultMTU`）；链路需允许 1320 内 UDP。
- 如果 8092/udp 被云防火墙拦截，客户端会退化为 relay 不可用；先验证公网 UDP 可达。

## 8. MCP 说明

- `docker-mcp-server` 的 `ssh://` 方式已停用；opencode 配置中的 `docker` MCP 保留但 `enabled=false`。
- VPS 远程操作改走 Hostodo MCP，已配置并完成 `tools/list`；实际能力/部署边界见 `docs/VPS-DEPLOY-VIA-HOSTODO.md`。
- 客户端 A/B 均为手动部署，见 `deploy-prod/README-client.md`。
