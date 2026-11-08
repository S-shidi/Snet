# VPS 部署路径：Hostodo MCP 能力与手动兜底

> 状态：只读探测；未部署、未执行 docker compose、未 SSH 登录、未修改 VPS。
> 目标：确认能否通过 Hostodo 远程 MCP 完成 VPS server + relay 部署；不能确认时给出用户手动 SSH 的兜底步骤。

## 1. Hostodo MCP 探测结果

端点：

```text
https://api.hostodo.com/mcp
```

已执行一次**不带 token**的 `tools/list` 只读请求：

```text
POST /mcp
{"jsonrpc":"2.0","id":1,"method":"tools/list"}

HTTP 401
{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"Bearer token required","data":{"code":"unauthenticated"}}}
```

结论：

- Hostodo MCP 端点存在且要求 Bearer token。
- 用户消息中没有提供 token；当前 opencode 配置中也没有 `hostodo` 条目。
- 因此 **tools/list 未成功，Hostodo 工具列表未知**。
- 不能判断是否支持 shell、Docker、文件上传/下载、端口/防火墙管理；**不做假设**。

## 2. MCP 配置现状

- 已备份：`/root/.config/opencode/opencode.jsonc.bak-hostodo-20261108-080035`
- 已有 `docker` MCP 条目保留，但 `enabled=false`（不再用 `docker-mcp-server` 的 `ssh://` 方式）。
- 未加入 `hostodo` 条目：缺少 Bearer token；不写入占位 token，避免误配置。
- 拿到 token 后再合并：

```jsonc
"hostodo": {
  "type": "remote",
  "url": "https://api.hostodo.com/mcp",
  "enabled": true,
  "headers": {
    "Authorization": "Bearer <token>"
  }
}
```

安全要求：token 不提交、不打印、不写入日志/命令历史。

## 3. VPS 部署路径结论

**当前只能确认：VPS 部署需用户手动 SSH；opencode 仅提供产物。**

原因：

1. Hostodo tools/list 因缺少 token 未成功，能力未知；
2. 不能假设其支持命令执行或 Docker；
3. 即使后续确认支持，也应先以只读工具/权限边界为准，再决定是否自动化。

## 4. 手动 SSH 部署步骤（兜底）

前提：用户提供 VPS SSH 权限，并把 `~/.ssh/id_ed25519.pub` 加入
`root@66.187.6.46:~/.ssh/authorized_keys`。

### 4.1 备份与清理旧部署

```bash
# 只读盘点
systemctl list-units --type=service | grep -i snet || true
systemctl status snet-server --no-pager 2>&1 | head -20 || true
ls -la /usr/local/snet /etc/snet 2>/dev/null || true
find / -name 'snet.db' 2>/dev/null || true
grep -r 'snet.uizhi' /etc/nginx /etc/caddy /etc/systemd 2>/dev/null || true
```

确认路径后备份：

```bash
mkdir -p /root/snet-old-backup-$(date +%Y%m%d)
# 备份旧 bbolt、/etc/snet、systemd units、TLS 证书路径
# 保留 SSH 配置、authorized_keys、TLS 证书/私钥
```

停止/卸载旧服务前，先记录 unit 文件和 env；不要删除证书和 SSH 配置。

### 4.2 安装 Docker 与部署目录

```bash
# 按 VPS 发行版安装 Docker Engine + Compose v2
# 然后：
mkdir -p /opt/snet
# 将 deploy-prod/docker/ 上传到 /opt/snet/docker/
```

### 4.3 生成 Relay v2 secret

```bash
umask 077
openssl rand -hex 32 > /opt/snet/docker/relay.secret
# 填入 /opt/snet/docker/.env 的 SNET_RELAY_SECRET，不打印
```

### 4.4 启动

```bash
cd /opt/snet/docker
cp .env.example .env
chmod 600 .env
# 填 SNET_RELAY_SECRET + SNET_RELAY_V2_ENDPOINTS
docker compose --env-file .env up -d --build
```

顺序：relay 先起并通过 `/healthz`，server 再起；两者 secret 必须一致。

### 4.5 防火墙 / 端口

```text
8090/tcp  公网  server HTTPS
8091/udp  公网  probe
8092/udp  公网  Relay v2
8094/tcp  仅容器内/本机  relay metrics（不映射）
```

### 4.6 验证

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
docker compose ps
docker compose exec relay wget -qO- http://127.0.0.1:8094/healthz
```

### 4.7 客户端

客户端 A/B 手动部署，见 `deploy-prod/manual-client/README.md`。
不要使用 docker-mcp-server 的 `ssh://` 自动部署。

## 5. 若后续 Hostodo tools/list 成功

拿到 token 后重新探测并记录实际工具：

- 是否有 `exec`/shell 类工具；
- 是否有 Docker/compose 工具；
- 是否有文件上传/下载；
- 是否有防火墙/端口工具；
- 是否有权限/主机选择参数。

在确认前，本文件的结论保持：**手动 SSH 是唯一已确认路径**。

## 6. 需要用户补充

1. Hostodo MCP Bearer token（只通过安全渠道提供，不提交）。
2. VPS SSH 公钥安装确认。
3. 旧部署清理授权（本次未清理）。
4. 两台客户端主机的架构/TUN/端口信息。
5. 是否允许后续通过 Hostodo MCP 执行写操作；若允许，需明确主机范围和回滚方式。
