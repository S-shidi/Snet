# VPS 部署路径：Hostodo MCP 能力与自动化边界

> 状态：已配置 Hostodo MCP 并完成只读 `tools/list` + `list_vms` + `get_vm` 探测。
> 未部署、未执行 docker compose、未执行 systemctl、未通过 MCP 执行 shell、未 SSH 登录 VPS。
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

**资源提醒**：目标 VPS 只有 1 vCPU / 1GB RAM。在 VPS 上直接 `docker compose build` 两个 Go 镜像可能 OOM；建议本机构建镜像后 `docker save` + 上传 + `docker load`，或分阶段/限内存构建。

## 4. 能力判断

| 能力 | 是否支持 | 说明 |
|---|---|---|
| 指定服务器执行 shell | **是** | `hostodo_exec_vm_command`，root 命令，最长 60s；长任务用 `hostodo_start_vm_command` |
| Docker / compose | **无专用工具** | 可通过 shell 执行 `docker`/`docker compose`；VPS 是否已安装 Docker 需执行 `docker --version` 确认（未执行） |
| 文件上传 | **是** | `create_artifact_upload` + `install_artifact`（presigned PUT，SHA-256 校验） |
| 文件下载 | **否（通用）** | 只有 `get_service_logs` 可读日志；没有通用 download 工具 |
| 防火墙 / 端口 | **无专用工具** | 可通过 shell 执行 `ufw`/`firewall-cmd`/`iptables`，需先确认系统工具 |
| systemd 服务管理 | **无直接工具** | `restart_service` 是 coming soon stub；可通过 shell `systemctl` |
| 日志 | **是** | `get_service_logs`（systemd 或 `/var/log/**`） |
| 异步长命令 | **是** | `start_vm_command` + output/log/cancel |

## 5. VPS 部署自动化边界

### 5.1 可全自动（经用户批准后，通过 MCP）

1. `hostodo_list_vms` / `get_vm` 确认目标 `ins::y5a3ro01rd`，`mcp_exec_enabled=true`。
2. `hostodo_exec_vm_command` 备份旧部署（tar `/usr/local/snet`、`/etc/snet` 到 `/root/snet-backup-*`）。
3. `hostodo_exec_vm_command` 检查 Docker；未装则按 OS 执行安装命令。
4. 本机打包 `deploy-prod/docker/`，`hostodo_create_artifact_upload` 上传，`hostodo_install_artifact` 落到 VPS。
5. `hostodo_exec_vm_command` 生成 secret 到 `/etc/snet/relay.secret`（不打印），写 `.env`（secret 从文件引用）。
6. `hostodo_start_vm_command` 执行 `docker compose up -d --build`，用 `get_vm_command_output` 轮询。
7. `hostodo_get_service_logs` / `exec_vm_command` 验证 `docker compose ps`、`curl /healthz`、8092/udp 监听。

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

## 6. 通过 MCP 部署步骤草案（不执行，待批准）

```text
1. list_vms / get_vm -> 确认 ins::y5a3ro01rd，exec=true
2. exec_vm_command:
   mkdir -p /root/snet-backup-YYYYMMDD
   tar czf /root/snet-backup-YYYYMMDD/snet-old.tgz /usr/local/snet /etc/snet 2>/dev/null || true
3. exec_vm_command:
   docker --version && docker compose version
   若未安装：按 OS 包管理器安装 Docker + compose plugin
4. 本机：tar czf snet-deploy.tgz -C deploy-prod docker
   create_artifact_upload(vm_id, filename, size_bytes, sha256, purpose=file)
   PUT 到 presigned URL
   install_artifact(artifact_id, /tmp/snet-deploy.tgz, mode=0600)
5. exec_vm_command:
   mkdir -p /opt/snet
   tar xzf /tmp/snet-deploy.tgz -C /opt/snet
6. exec_vm_command:
   umask 077; openssl rand -hex 32 > /etc/snet/relay.secret
   写 /opt/snet/docker/.env（SNET_RELAY_SECRET 从文件读取；SNET_RELAY_V2_ENDPOINTS=snet.uizhi.eu.org:8092）
7. start_vm_command:
   cd /opt/snet/docker && docker compose --env-file .env up -d --build
   get_vm_command_output 轮询
8. 验证：
   docker compose ps
   curl -fsS https://snet.uizhi.eu.org:8090/healthz
   docker compose exec relay wget -qO- http://127.0.0.1:8094/healthz
   外部确认 8092/udp 可达
```

注意：`exec_vm_command` 最长 60s；`docker compose build` 用 `start_vm_command`。secret 不在命令回显/日志中出现；artifact 的 presigned URL 也不写入报告。

## 7. 当前结论

- Hostodo MCP **支持 root shell + 异步长命令 + 文件上传/安装 + 日志**；目标 VPS `mcp_exec_enabled=true`。
- **没有专用 Docker/防火墙/systemd 管理工具**；这些通过 shell 执行，需确认 VPS 已安装对应工具。
- VPS 部署**可以大部分通过 MCP 自动化**（备份、安装 Docker、上传 compose、写 secret、compose up、验证），但需要用户批准写操作，并处理 1GB RAM 构建风险、TLS 路径/防火墙和外部 UDP 验证。
- 本次未执行任何部署或 shell 命令；仅调用只读 `tools/list`、`list_vms`、`get_vm`。

## 8. 仍需用户确认

1. 是否批准通过 Hostodo MCP 执行写操作（备份、安装 Docker、compose up）。
2. 旧部署清理/保留范围。
3. VPS 是否已有 Docker；若无，允许用哪种安装方式。
4. 8092/udp 的外部可达性/云防火墙。
5. 是否采用本机构建镜像 + `docker save/load`，避免 1GB VPS OOM。
