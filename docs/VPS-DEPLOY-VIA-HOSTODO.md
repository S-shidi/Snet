# VPS 部署路径：Hostodo MCP 能力与自动化边界

> 状态：已配置 Hostodo MCP，`vms:exec` token 验证成功，并完成只读 `exec_vm_command` 探测。
> 未部署、未执行 docker compose、未执行 systemctl 写操作、未停旧服务、未 SSH 登录 VPS。
> Bearer token 只写入 `/root/.config/opencode/opencode.jsonc`（仓库外）；本文件不包含 token。

## 1. Hostodo MCP 配置

- 配置文件：`/root/.config/opencode/opencode.jsonc`
- 配置已合并 `hostodo` remote MCP，`enabled=true`。
- 已有 `docker` MCP 保留，`enabled=false`（不再使用 `docker-mcp-server` 的 `ssh://`）。
- 备份：
  - `/root/.config/opencode/opencode.jsonc.bak-hostodo-20261108-080035`
  - `/root/.config/opencode/opencode.jsonc.bak-hostodo-20261108-081742`
- 配置文件在 `/workspace/Snet` 之外；token 不会进入本仓库。

## 2. tools/list 结果

`POST https://api.hostodo.com/mcp` + Bearer token：

```text
HTTP 200
56 tools
```

### 2.1 VM / shell / 日志

| 工具 | 能力 | 参数摘要 |
|---|---|---|
| `hostodo_list_vms` | 列出 VPS 与能力标记 | 无 |
| `hostodo_get_vm` | 查看单台 VPS 元数据 | `vm_id:string` |
| `hostodo_power_vm` | 开机/关机/重启/停止/重置 | `vm_id:string`, `action:start\|shutdown\|reboot\|stop\|reset` |
| `hostodo_exec_vm_command` | 通过 QEMU guest agent 执行 root 命令，**最长 60s** | `vm_id:string`, `command:string`, `timeout_seconds:int` |
| `hostodo_get_service_logs` | 读 systemd journal 或 `/var/log/**` 文件日志，最大 256KB | `vm_id`, `service`, `log_path`, `lines`, `max_bytes` |
| `hostodo_set_vm_exec_enabled` | 开启/关闭 MCP root 执行，需要精确确认短语 | `vm_id`, `enabled:bool`, `confirmation:string` |
| `hostodo_reinstall_vm` | 破坏性重装 VPS（不是应用部署） | `vm_id`, `template_id`, `confirmation`, `ssh_key_id` |
| `hostodo_rename_vm` | 重命名 VPS | `vm_id`, `hostname`, `confirmation` |

### 2.2 文件上传 / 长命令

| 工具 | 能力 | 参数摘要 |
|---|---|---|
| `hostodo_create_artifact_upload` | 创建 15 分钟 presigned PUT 上传槽，上传文件到 VPS | `vm_id`, `filename`, `size_bytes`, `sha256`, `purpose:file\|config\|script` |
| `hostodo_install_artifact` | 下载已上传 artifact 到 VPS，校验 SHA-256，设置 owner/mode，原子移动 | `artifact_id`, `destination_path`, `mode`, `owner` |
| `hostodo_start_vm_command` | 启动长命令（构建/安装/重启），返回 command_id | `vm_id`, `command`, `timeout_seconds`, `label`, `working_directory` |
| `hostodo_get_vm_command_output` | 查询异步命令状态和 stdout/stderr 尾部 | `vm_id`, `command_id`, `stdout_tail_bytes`, `stderr_tail_bytes` |
| `hostodo_read_vm_command_logs` | 按 offset 增量读异步日志 | `vm_id`, `command_id`, offsets, `max_bytes` |
| `hostodo_cancel_vm_command` | 取消异步命令 | `vm_id`, `command_id` |

### 2.3 购买 / 资源池 / 工单

- `hostodo_list_deployment_options`、`hostodo_quote_deployment`、`hostodo_create_deployment`、`hostodo_get_deployment`
- `hostodo_list_resource_pools`、`hostodo_get_resource_pool`、`hostodo_list_pool_options`、`hostodo_create_pool_vm`、`hostodo_create_resource_pool`、`hostodo_upgrade_resource_pool`
- `hostodo_list_tickets`、`hostodo_get_ticket`、`hostodo_create_ticket`、`hostodo_reply_ticket`、`hostodo_close_ticket`
- `hostodo_list_templates`

### 2.4 Coming soon / stub（不要依赖）

以下工具描述明确写 `Coming soon`，只返回 structured `coming_soon`，不执行实际操作：

- `hostodo_rollback_deployment`
- `hostodo_restart_service`（**不是**可用的 systemctl 重启工具）
- `hostodo_list_invoices`、`hostodo_get_invoice`、`hostodo_pay_invoice`
- `hostodo_list_payment_methods`、`hostodo_add_payment_method`、`hostodo_get_account_balance`、`hostodo_apply_credit`
- `hostodo_upload_ticket_attachment`
- DNS：`hostodo_list_dns_zones`、`hostodo_create_dns_record`、`hostodo_update_dns_record`、`hostodo_delete_dns_record`、`hostodo_list_rdns`、`hostodo_set_rdns`
- Snapshot：`hostodo_list_snapshots`、`hostodo_create_snapshot`、`hostodo_restore_snapshot`
- Monitoring：`hostodo_get_vm_metrics`、`hostodo_get_bandwidth_usage`、`hostodo_get_incidents`、`hostodo_check_vm_health`
- Access：`hostodo_list_ssh_keys`、`hostodo_add_ssh_key`、`hostodo_remove_ssh_key`

## 3. 目标 VPS 元数据（只读）

`hostodo_list_vms` + `hostodo_get_vm` 返回：

```text
id:              ins::y5a3ro01rd
hostname:        us-tpa01-8304f360
primary_ipv4:    66.187.6.46
status:          provisioned
power_state:     running
mcp_exec_enabled: true
location:        TPA01
plan:            EPYC-1G1C16GN
vcpu:            1
ram_mb:          1024
disk_gb:         20
bandwidth_gb:    3072
```

另一台 `ins::941aeini7a`（SDVPS，158.51.126.58）为 suspended，`mcp_exec_enabled=false`；不是本次目标。

**资源提醒**：目标 VPS 只有 1 vCPU / 1GB RAM。Dockerfile 已改为 **COPY 预编译二进制**（`dist-deploy/linux-${BIN_ARCH}/{server,relay}`），不在 VPS 上运行 Go 构建；仍需要构建一个薄 Alpine 镜像（或本机构建镜像后 `docker save`/上传/`docker load`）。

### 3.1 2026-11-08 只读探测结果（exec 成功）

最小验证：

```text
uname -m && id && free -m | head -2
exit_code=0
x86_64
uid=0(root) gid=0(root)
Mem: 967 total, 806 used, 97 free, 161 available
```

完整只读探测：

```text
OS:        Debian GNU/Linux 13 (trixie)
Kernel:    6.12.113+deb13-cloud-amd64
Arch:      x86_64 (amd64)
CPU:       1
Memory:    967 MB total, 814 MB used, 88 MB free, 153 MB available
Swap:      4095 MB total, 722 MB used, 3373 MB free (/swapfile)
Disk:      /dev/sda1 20G, 13G used, 6.3G avail, 67%
Docker:    /usr/bin/docker, Docker 29.7.2, Compose v5.5.0, service active
```

旧部署：

```text
snet-server.service        active (running) PID 357564
snet-runtime-http.service  active (running)
binary: /usr/local/snet/bin/server
db:     /var/lib/snet/snet.db
unit:   /etc/systemd/system/snet-server.service
env:    /etc/snet-server.env
command:
  server -addr 0.0.0.0:8090 -probe-addr [::]:8091
         -db /var/lib/snet/snet.db
         -tls-cert /usr/local/snet/certs/server.pem
         -tls-key /usr/local/snet/certs/server-key.pem
         -relay-host 66.187.6.46 -relay-base 51820 -relay-count 256
         -zombie-ttl 72h
```

bbolt 发现：

```text
/var/lib/snet/snet.db
/usr/local/snet/backups/1.3.0-20261027_015222/snet.db
/usr/local/snet/backups/1.2.0-20261027_000755/snet.db
/root/snet-client-removal-20261026_162417/snet.db
```

端口：

```text
udp *:8091 users:(("server",pid=357564,fd=6))
tcp *:8090 users:(("server",pid=357564,fd=7))
```

- 8090/8091 都由旧 `server` 进程直接监听，不是 nginx/caddy。
- 8092/8094 当前未监听，可用于新 relay。
- `systemctl cat nginx/caddy` 未发现相关配置输出。

TLS：

```text
/etc/letsencrypt/live/snet.uizhi.eu.org/fullchain.pem
/etc/letsencrypt/live/snet.uizhi.eu.org/privkey.pem
/usr/local/snet/certs/server.pem -> /etc/letsencrypt/live/snet.uizhi.eu.org/fullchain.pem
/usr/local/snet/certs/server-key.pem -> /etc/letsencrypt/live/snet.uizhi.eu.org/privkey.pem
```

实际 archive 文件：

```text
/etc/letsencrypt/archive/snet.uizhi.eu.org/fullchain2.pem
/etc/letsencrypt/archive/snet.uizhi.eu.org/privkey2.pem
```

Docker 现有容器（与本项目无关）：

```text
penpot-penpot-* (frontend/backend/exporter/postgres/valkey)
dpanel
```

没有 snet 容器。

**资源提醒**：目标 VPS 只有 1 vCPU / 1GB RAM；当前可用内存约 153MB，swap 已 4GB（约 3.3GB free）。Dockerfile 已改为 COPY 预编译二进制，不在 VPS 跑 Go build；仍建议给 compose 容器设置 300M-400M memory limit，并优先用本机构建镜像 + `docker save/load`，避免薄镜像 build 期间的峰值内存压力。

## 4. 能力判断

| 能力 | 是否支持 | 说明 |
|---|---|---|
| 指定服务器执行 shell | **是** | `hostodo_exec_vm_command`，root 命令，最长 60s；已验证 `vms:exec` 成功 |
| Docker / compose | **无专用工具** | 可通过 shell 执行；VPS 已装 Docker 29.7.2 + Compose v5.5.0，service active |
| 文件上传 | **是** | `create_artifact_upload` + `install_artifact`；`vms:exec` 已验证 |
| 文件下载 | **否（通用）** | 只有 `get_service_logs` 可读日志；没有通用 download 工具 |
| 防火墙 / 端口 | **无专用工具** | 可通过 shell 执行 `ufw`/`firewall-cmd`/`iptables`，需先确认系统工具 |
| systemd 服务管理 | **无直接工具** | `restart_service` 是 coming soon stub；可通过 shell `systemctl` |
| 日志 | **是** | `get_service_logs`（systemd 或 `/var/log/**`） |
| 异步长命令 | **是** | `start_vm_command` + output/log/cancel |

## 5. VPS 部署自动化边界

### 5.1 已验证 `vms:exec`：可全自动（经用户批准后，通过 MCP）

1. `hostodo_list_vms` / `get_vm` 确认目标 `ins::y5a3ro01rd`，`mcp_exec_enabled=true`。
2. `hostodo_exec_vm_command` 备份旧部署（tar `/usr/local/snet`、`/etc/snet` 到 `/root/snet-backup-*`）。
3. `hostodo_exec_vm_command` 检查 Docker；未装则按 OS 执行安装命令。
4. 本机打包 `deploy-prod/docker/` + `dist-deploy/linux-<arch>/`，`hostodo_create_artifact_upload` 上传，`hostodo_install_artifact` 落到 VPS。
5. `hostodo_exec_vm_command` 生成 secret 到 `/etc/snet/relay.secret`（不打印），写 `.env`（secret 从文件引用）。
6. `hostodo_start_vm_command` 执行 `docker compose up -d --build`（薄镜像只 COPY 预编译二进制），用 `get_vm_command_output` 轮询。
7. `hostodo_get_service_logs` / `exec_vm_command` 验证 `docker compose ps`、`curl /healthz`、8092/udp 监听。

当前 token 已通过 `vms:exec` 验证；上述步骤技术上可执行，但仍需用户批准写操作。

### 5.2 半自动（MCP + 用户确认）

- TLS 证书路径确认（`/etc/letsencrypt/live/snet.uizhi.eu.org/...`）；如路径不同需改 `.env`。
- DNS 指向确认（当前 `snet.uizhi.eu.org` → `66.187.6.46` 已由公网探测确认）。
- 防火墙/云安全组放行 8090/tcp、8091/udp、8092/udp；MCP 无专用防火墙工具，需 shell 或用户控制台。
- VPS 资源不足时的镜像构建策略（本地 build + `docker save/load`）。
- Docker 已安装且 service active，不需要首次安装；若后续升级 Docker，需用户批准。

### 5.3 8090 部署路径决策

- 实际监听：`server` PID 357564，systemd unit `snet-server.service`，直接监听 8090/8091；不是 nginx/caddy。
- 因此新 server 方案：**停旧 `snet-server.service` → compose server 直接绑定 8090/8091，使用 `-tls-cert/-tls-key` 直连 TLS**。
- 不需要保留 nginx/caddy 反代；`systemctl cat nginx/caddy` 未发现相关配置。
- `snet-runtime-http.service` 仍 active；新 server/relay 不需要它，是否停用/保留由用户决定（先备份）。
- 复用 TLS 路径：`/etc/letsencrypt/live/snet.uizhi.eu.org/fullchain.pem` + `privkey.pem`；compose 默认挂载 `/etc/letsencrypt`，无需改路径。

### 5.4 必须用户手动 / 明确授权

- 批准清理旧部署（destructive）。
- 批准通过 MCP 执行写操作（本次未执行）。
- 若 `mcp_exec_enabled` 被关闭，先由用户确认 `set_vm_exec_enabled`（精确短语）。
- 公开 UDP 8092 的外部可达性最终确认（从外部网络探测）。
- 证书续期/证书私钥处理（不要通过 MCP 回传私钥）。
- 两台客户端主机的 TUN/root/端口检查（客户端手动部署）。

## 6. 部署顺序草案（不执行，待批准）

```text
0. 已完成只读确认：Debian 13 x86_64(amd64), Docker 29.7.2 + Compose v5.5.0 active,
   8090/8091 由旧 server 监听，8092/8094 空闲，TLS 路径已确认
1. 备份旧部署：tar /usr/local/snet /etc/snet /etc/snet-server.env /var/lib/snet/snet.db
   到 /root/snet-backup-YYYYMMDD（先确认备份可读）
2. 停旧 snet-server.service（用户批准后）；如保留 snet-runtime-http，先确认其用途
3. 上传：
   - dist-deploy/linux-<arch>/{server,relay}
   - deploy-prod/docker/（Dockerfile/compose/.env.example）
4. 生成 secret：umask 077; openssl rand -hex 32 > /etc/snet/relay.secret（不打印）
5. 写 .env：SNET_RELAY_SECRET 从文件引用；SNET_RELAY_V2_ENDPOINTS=snet.uizhi.eu.org:8092；
   BIN_ARCH=amd64（uname -m 已确认 x86_64）
6. docker compose up -d --build（薄镜像 COPY 预编译二进制；不在 VPS 跑 Go build）
   建议 compose 容器 memory limit 300M-400M；当前 swap 4G，无需再加
7. 验证：docker compose ps；curl https://snet.uizhi.eu.org:8090/healthz；
   relay 127.0.0.1:8094/healthz；外部 8092/udp 可达
```

前置判断：

- 8090/8091 由旧 `snet-server` systemd 二进制监听；不是 nginx/caddy。停旧 unit 后 compose server 可直接绑定 8090/8091。
- TLS 路径已确认：`/etc/letsencrypt/live/snet.uizhi.eu.org/{fullchain,privkey}.pem`；compose 挂载 `/etc/letsencrypt` 即可。
- 1GB VPS 不运行 Go build；Dockerfile 已改为 COPY 预编译二进制。Docker 已安装；薄镜像 build 仍会占用少量内存，建议本机构建 + `docker save/load` 或设置 300M-400M memory limit。
- 架构已确认：`x86_64` → `BIN_ARCH=amd64`。
- 现有 4GB swap（约 3.3GB free），不需要再新增 1-2GB；注意当前可用内存约 153MB，且已有 penpot/dpanel 容器。

## 7. 当前结论

- Hostodo MCP `vms:exec` 已成功；目标 VPS 为 Debian 13 x86_64/amd64，1 vCPU / 967MB RAM / 4GB swap / 20GB disk（6.3G free）。
- Docker 29.7.2 + Compose v5.5.0 已安装且 active；8090/8091 由旧 `snet-server` systemd 二进制监听，8092/8094 空闲；TLS 路径已确认。
- **没有专用 Docker/防火墙/systemd 管理工具**；Docker/compose/systemctl/防火墙需通过 shell 执行。
- Dockerfile 已改为 COPY `dist-deploy/linux-amd64/{server,relay}`，避免在 1GB VPS 上跑 Go build；部署时仍需用户批准停旧服务、备份/清理旧部署、写 secret、compose up 和防火墙放行。
- 本次只执行只读 `tools/list`、`list_vms`、`get_vm`、`exec_vm_command`；未部署、未停服务、未写 VPS。

## 8. 仍需用户确认

1. 是否批准通过 Hostodo MCP 执行写操作（备份、安装 Docker、compose up）。
2. 旧部署清理/保留范围。
3. VPS 是否已有 Docker；若无，允许用哪种安装方式。
4. 8092/udp 的外部可达性/云防火墙。
5. 是否采用本机构建镜像 + `docker save/load`，避免 1GB VPS OOM。

## 9. 2026-11-08 实际部署结果（已执行）

### 备份

```text
备份路径: /root/snet-backup-20261108_084059
内容:
  snet.db                 131072 bytes
  snet-server.env            123 bytes
  snet-server.service.txt    644 bytes
总大小: 76K
```

旧服务已停止并 disable：

```text
snet-server.service        inactive
snet-runtime-http.service  inactive
```

8090/8091 已释放；未动 penpot/dpanel 等其他容器；旧备份保留。

### 上传与部署

- 上传到 `/opt/snet-deploy/`：
  - `Dockerfile.server`
  - `Dockerfile.relay`
  - `compose.yaml`（为平铺目录改为 `context: .` / `dockerfile: Dockerfile.*`）
  - `dist-deploy/linux-amd64/server`
  - `dist-deploy/linux-amd64/relay`
- 远程 sha256 与本地一致：
  - server `5791c664e436120b89a0e56accbdf4072a2406dae3b1fcda65493b0e16753c54`
  - relay `ab6e4bb08deee32b3aec0f90bae9499ea909cb4cf8f118e3fd6d78b444cf8f96`
- `/opt/snet-deploy/relay.secret` 与 `.env` 已生成，均为 0600；未打印 secret。
- `docker compose --env-file .env up -d --build` 异步执行成功：
  - duration 40s
  - exit_code 0
  - 未 OOM
  - 构建仅 COPY 预编译二进制，未在 VPS 运行 Go build

### 验证

```text
docker compose ps
snet-relay   Up (healthy)   0.0.0.0:8092->8092/udp
snet-server  Up             0.0.0.0:8090->8090/tcp, 0.0.0.0:8091->8091/udp
```

```text
relay log: snet-relay 1.3.0 listening on [::]:8092 (metrics 127.0.0.1:8094)
server log: relay v2: tickets for snet.uizhi.eu.org:8092
server log: admin: no credentials configured yet; initialize via /admin
server log: require-device-auth: false
```

端口：

```text
0.0.0.0:8090 tcp  docker-proxy -> server
0.0.0.0:8091 udp  docker-proxy -> server
0.0.0.0:8092 udp  docker-proxy -> relay
8094 未发布，仅容器内 127.0.0.1
```

本地/外部 healthz：

```json
{"apiVersion":1,"buildDate":"unknown","commit":"unknown","serverVersion":"1.3.0","status":"ok"}
```

- `buildDate/commit` 为 `unknown`：预编译二进制未注入 ldflags；`serverVersion=1.3.0`。
- TLS 仍为 Let's Encrypt `snet.uizhi.eu.org`，证书未更换。
- relay metrics（容器内）：

```json
{"status":"ok","sessions":0,"packetsIn":0,"packetsOut":0}
```

`docker stats --no-stream`：

```text
snet-server 1.961MiB / 967.9MiB
snet-relay  6.758MiB / 967.9MiB
```

均远低于 300M；penpot/dpanel 未被操作。

### 当前状态与后续

- 新 server/relay 已运行；Relay v2 endpoint 已配置为 `snet.uizhi.eu.org:8092`。
- 管理台当前无 admin 凭据：需要首次打开 `https://snet.uizhi.eu.org:8090/admin` 完成 bootstrap。
- `require-device-auth=false`；如要启用设备授权，需要后续调整 server 启动参数/env 并重启。
- 云防火墙需用户放行 8090/tcp、8091/udp、8092/udp；8094 不对外。

### 回滚命令（未使用）

```bash
cd /opt/snet-deploy
docker compose --env-file .env down
cp /root/snet-backup-20261108_084059/snet.db /var/lib/snet/snet.db
cp /root/snet-backup-20261108_084059/snet-server.env /etc/snet-server.env
systemctl enable --now snet-server snet-runtime-http
```

### 外部验证命令（给用户）

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
# UDP 8092（按本机工具选择）
nc -u -z -v snet.uizhi.eu.org 8092
# 客户端手动部署后：
snetctl -ctl http://127.0.0.1:19432 status
snetctl -ctl http://127.0.0.1:19432 peers <nid>
```
