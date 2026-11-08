# VPS 部署路径：Hostodo MCP 能力与自动化边界

> 状态：已配置 Hostodo MCP 并完成只读 `tools/list` + `list_vms` + `get_vm` 探测。
> 2026-11-08 尝试 `hostodo_exec_vm_command` 只读探测时返回 **403 `vms:exec` scope 缺失**；未执行任何 shell 命令。
> 未部署、未执行 docker compose、未执行 systemctl、未 SSH 登录 VPS。
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

### 3.1 2026-11-08 exec 只读探测结果

尝试：

```text
hostodo_exec_vm_command(vm_id=ins::y5a3ro01rd, command=<read-only probe>)
HTTP 403
error.code: -32003
error.message: session lacks required scope: vms:exec
required_scope: vms:exec
```

因此以下**未获取，不能假设**：

- `uname -m` / OS 发行版 / nproc / free / df
- Docker 是否已安装、docker compose 版本、docker service 状态
- 旧 snet systemd units、`/usr/local/snet`、`/etc/snet`、`snet.db`
- TLS 证书实际路径、nginx/caddy 是否占用 8090
- 8090/8091/8092/8094 的实际监听进程
- 目标 VPS 的 CPU 架构（Hostodo plan 是 EPYC，但未用 `uname -m` 确认）

已有公网只读证据：`https://snet.uizhi.eu.org:8090/healthz` 返回 1.3.0；TLS 为 Let's Encrypt。
`8090` 当前由谁监听、旧部署内部结构、证书路径均**待 vms:exec scope 或用户手动 SSH 确认**。

## 4. 能力判断

| 能力 | 是否支持 | 说明 |
|---|---|---|
| 指定服务器执行 shell | **工具支持；当前 token 不可用** | `hostodo_exec_vm_command`，root 命令，最长 60s；当前 token 缺 `vms:exec`，返回 403 |
| Docker / compose | **无专用工具** | 可通过 shell 执行 `docker`/`docker compose`；但当前 token 缺 `vms:exec`，无法执行；VPS 是否已安装 Docker 未确认 |
| 文件上传 | **工具支持；当前 token 不可用** | `create_artifact_upload` + `install_artifact`；需 `vms:exec`，当前 token 缺 scope |
| 文件下载 | **否（通用）** | 只有 `get_service_logs` 可读日志；没有通用 download 工具 |
| 防火墙 / 端口 | **无专用工具** | 可通过 shell 执行 `ufw`/`firewall-cmd`/`iptables`，需先确认系统工具 |
| systemd 服务管理 | **无直接工具** | `restart_service` 是 coming soon stub；可通过 shell `systemctl` |
| 日志 | **是** | `get_service_logs`（systemd 或 `/var/log/**`） |
| 异步长命令 | **工具支持；当前 token 不可用** | `start_vm_command` + output/log/cancel；需 `vms:exec` |

## 5. VPS 部署自动化边界

### 5.1 若补齐 `vms:exec` scope：可全自动（经用户批准后，通过 MCP）

1. `hostodo_list_vms` / `get_vm` 确认目标 `ins::y5a3ro01rd`，`mcp_exec_enabled=true`。
2. `hostodo_exec_vm_command` 备份旧部署（tar `/usr/local/snet`、`/etc/snet` 到 `/root/snet-backup-*`）。
3. `hostodo_exec_vm_command` 检查 Docker；未装则按 OS 执行安装命令。
4. 本机打包 `deploy-prod/docker/` + `dist-deploy/linux-<arch>/`，`hostodo_create_artifact_upload` 上传，`hostodo_install_artifact` 落到 VPS。
5. `hostodo_exec_vm_command` 生成 secret 到 `/etc/snet/relay.secret`（不打印），写 `.env`（secret 从文件引用）。
6. `hostodo_start_vm_command` 执行 `docker compose up -d --build`（薄镜像只 COPY 预编译二进制），用 `get_vm_command_output` 轮询。
7. `hostodo_get_service_logs` / `exec_vm_command` 验证 `docker compose ps`、`curl /healthz`、8092/udp 监听。

当前 token **缺少 `vms:exec`**，上述第 2-7 步尚不能执行。

### 5.2 半自动（MCP + 用户确认）

- TLS 证书路径确认（`/etc/letsencrypt/live/snet.uizhi.eu.org/...`）；如路径不同需改 `.env`。
- DNS 指向确认（当前 `snet.uizhi.eu.org` → `66.187.6.46` 已由公网探测确认）。
- 防火墙/云安全组放行 8090/tcp、8091/udp、8092/udp；MCP 无专用防火墙工具，需 shell 或用户控制台。
- VPS 资源不足时的镜像构建策略（本地 build + `docker save/load`）。
- 首次安装 Docker 若 shell/权限/OS 包管理不支持，需要用户手动。

### 5.3 必须用户手动 / 明确授权

- 批准清理旧部署（destructive）。
- 批准通过 MCP 执行写操作（本次未执行）。
- 若 `mcp_exec_enabled` 被关闭，先由用户确认 `set_vm_exec_enabled`（精确短语）。
- 公开 UDP 8092 的外部可达性最终确认（从外部网络探测）。
- 证书续期/证书私钥处理（不要通过 MCP 回传私钥）。
- 两台客户端主机的 TUN/root/端口检查（客户端手动部署）。

## 6. 部署顺序草案（不执行，待批准）

```text
0. 只读确认：uname -m / Docker / 旧 snet units / 8090 listener / TLS 路径 / 端口
   （当前被 vms:exec 403 阻塞）
1. 备份旧部署：tar /usr/local/snet /etc/snet /root/snet-backup-YYYYMMDD
2. 停旧 snet-server（用户批准后）
3. 上传：
   - dist-deploy/linux-<arch>/{server,relay}
   - deploy-prod/docker/（Dockerfile/compose/.env.example）
4. 生成 secret：umask 077; openssl rand -hex 32 > /etc/snet/relay.secret（不打印）
5. 写 .env：SNET_RELAY_SECRET 从文件引用；SNET_RELAY_V2_ENDPOINTS=snet.uizhi.eu.org:8092；
   BIN_ARCH=<amd64|arm64> 按 uname -m 确认
6. docker compose up -d --build（薄镜像 COPY 预编译二进制；不在 VPS 跑 Go build）
7. 验证：docker compose ps；curl https://snet.uizhi.eu.org:8090/healthz；
   relay 127.0.0.1:8094/healthz；外部 8092/udp 可达
```

前置判断：

- 若 8090 已被 nginx/caddy 监听，必须先确认反代配置；不能直接让 server 绑定 8090。按 `ss -lntup` 实际输出决定停旧服务/改端口/改反代。
- TLS 证书路径必须确认后再写 compose 挂载；当前公网证书是 Let's Encrypt，但内部路径未探测。
- 1GB VPS 不运行 Go build；Dockerfile 已改为 COPY 预编译二进制。薄镜像 build 仍需要 Docker；若 Docker 未装，先装 Docker。
- 目标架构未确认；Dockerfile 默认 `BIN_ARCH=amd64`，`uname -m` 为 `aarch64` 时必须设 `BIN_ARCH=arm64`。

## 7. 当前结论

- Hostodo MCP 工具面支持 root shell、异步长命令、文件上传/安装、日志；目标 VPS `mcp_exec_enabled=true`。
- **但当前 token 缺 `vms:exec` scope**：`hostodo_exec_vm_command` 返回 403，无法执行任何只读/写 shell；因此 OS、Docker、旧部署、TLS 路径、8090 监听者、端口占用、架构均未确认。
- **没有专用 Docker/防火墙/systemd 管理工具**；即使有 scope，也需要 shell 执行并确认 VPS 已安装 Docker/防火墙工具。
- Dockerfile 已改为 COPY `dist-deploy/linux-${BIN_ARCH}/{server,relay}`，避免在 1GB VPS 上跑 Go build；但薄镜像 build 仍需 Docker，且架构需 `uname -m` 确认。
- 本次未执行任何部署或 shell 命令；仅调用只读 `tools/list`、`list_vms`、`get_vm`，以及被 403 拒绝的 `exec_vm_command` 尝试。

## 8. 仍需用户确认

1. 是否批准通过 Hostodo MCP 执行写操作（备份、安装 Docker、compose up）。
2. 旧部署清理/保留范围。
3. VPS 是否已有 Docker；若无，允许用哪种安装方式。
4. 8092/udp 的外部可达性/云防火墙。
5. 是否采用本机构建镜像 + `docker save/load`，避免 1GB VPS OOM。
