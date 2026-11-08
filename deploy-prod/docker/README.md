# SNET 生产 Docker Compose（VPS：server + relay）

目标：在 VPS 上用 Docker Compose 部署 `server` 和 `relay`，启用 Relay v2。
本目录不提交真实 `.env`；`.env` 在 VPS 上生成并保持 0600。

## 1. 前置

- Docker Engine + Docker Compose v2
- 可写目录：`deploy-prod/docker/`
- TLS 证书：默认挂载 `/etc/letsencrypt` 到容器只读
  - 默认路径：`/etc/letsencrypt/live/snet.uizhi.eu.org/fullchain.pem` 和 `privkey.pem`
  - 路径不同则在 `.env` 设置 `TLS_CERT_PATH` / `TLS_KEY_PATH`
- 公网 DNS：`snet.uizhi.eu.org` 指向 VPS
- 防火墙：放行 8090/tcp、8091/udp、8092/udp；不要放行 8094

`server` 支持直接 TLS：`cmd/server/main.go` 有 `-tls-cert` / `-tls-key`；Compose 已把这两个参数传入，不需要 nginx/caddy 反代。若将来改为反代，需另加 nginx 服务并让 server 只监听内网 HTTP。

## 2. 生成 secret 与 .env

在 VPS 的 `deploy-prod/docker/`：

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
```

relay 和 server 必须使用**同一个** `SNET_RELAY_SECRET`。

## 3. 构建与启动

```bash
cd deploy-prod/docker
docker compose --env-file .env up -d --build
```

顺序由 Compose 保证：

1. `relay` 先启动并通过 `/healthz` 健康检查；
2. `server` 等 `relay` healthy 后启动；
3. `server` 读取 `SNET_RELAY_V2_ENDPOINTS` + `SNET_RELAY_SECRET`，签发 Relay v2 ticket。

查看状态：

```bash
docker compose ps
docker compose logs -f relay server
```

## 4. 端口 / 防火墙

| 端口 | 协议 | 暴露 | 用途 |
|---|---|---|---|
| 8090 | TCP | 公网 | server HTTPS API / Web |
| 8091 | UDP | 公网 | public-IP probe |
| 8092 | UDP | 公网 | Relay v2 |
| 8094 | TCP | **仅容器网络/本机** | relay metrics/healthz，不映射 |

8094 只用于 Compose healthcheck 和容器内排障；不要发布到公网。

## 5. 验证

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
docker compose ps
docker compose exec relay wget -qO- http://127.0.0.1:8094/healthz
```

预期：

- `/healthz` 返回 `serverVersion` 和 `status:"ok"`；
- `relay` 和 `server` 都是 running/healthy；
- server 日志包含 Relay v2 ticket 配置；
- 客户端 `/ctl/status` 能看到 `relayPath`，`/peers` 返回 `RelayV2.Enabled=true` 和 endpoint `snet.uizhi.eu.org:8092`。

## 6. 数据与备份

- server bbolt：volume `snet-data` → 容器 `/data/snet.db`
- 备份：`docker run --rm -v <project>_snet-data:/data -v "$PWD":/backup alpine tar czf /backup/snet-data.tgz -C /data .`
- 证书：`/etc/letsencrypt` 只读挂载；certbot 续期后容器读取新文件，无需重建镜像。

## 7. 注意

- 不要在 `.env` 中提交真实 secret；`.gitignore` 已忽略 `deploy-prod/docker/.env`。
- relay 必须先起，secret 必须一致；否则 server 签发的 ticket 无法被 relay 验证。
- Relay v2 使用 MTU 1320（`protocol.DefaultMTU`）；链路需允许 1320 内 UDP。
- 如果 8092/udp 被云防火墙拦截，客户端会退化为 relay 不可用；先验证公网 UDP 可达。

## 8. MCP 说明

- `docker-mcp-server` 的 `ssh://` 方式已停用；opencode 配置中的 `docker` MCP 保留但 `enabled=false`。
- VPS 远程操作改走 Hostodo MCP，已配置并完成 `tools/list`；实际能力/部署边界见 `docs/VPS-DEPLOY-VIA-HOSTODO.md`。
- 客户端 A/B 均为手动部署，见 `deploy-prod/manual-client/README.md`。
