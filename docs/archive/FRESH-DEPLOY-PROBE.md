# 全新部署探测与 Relay v2 启用准备

> 状态：只读探测；未清理/停止旧部署，未部署，未启动 server/relay/snetd，未改配置。
> 目标：VPS `snet.uizhi.eu.org` 全新部署 server + Relay v2；两台不同外网服务器部署 snetd；全新项目无旧客户端兼容包袱，直接启用 Relay v2。
> 结论先行：**远程三节点探测被 SSH 信息缺失阻塞**；VPS 公网面可读，内部旧部署/端口/TUN 未探测。

## 0. 需要用户提供（当前阻塞）

| 项 | 状态 |
|---|---|
| 三台 SSH 地址（IP:端口） | **缺失** |
| 三台用户名 | **缺失** |
| 认证方式/密钥路径 | **缺失**；本机 `~/.ssh/` 为空 |
| 是否有 root/sudo | **未知** |
| Relay 部署点 | 未确认：与 VPS 同机，还是独立第三台 |
| 公网 UDP 8092 可达性/防火墙 | 未确认 |
| 旧 bbolt/证书/配置保留或备份要求 | 未确认 |

在补齐 SSH 信息前，第 1、2、3 步的远程探测无法执行。

## 1. opencode SSH 出口

```text
ssh: /usr/bin/ssh
scp: /usr/bin/scp
OpenSSH_9.6p1, OpenSSL 3.5.6
~/.ssh/: 空目录（无 config、无 known_hosts、无密钥）
```

- 未执行 `ssh <user>@<host>`：没有地址、用户名和密钥，避免猜测凭据。
- 需要用户提供三台目标；若密钥在别处，提供路径或挂载方式。

## 2. 三节点探测表

| 节点 | SSH | 架构 | TUN | 端口 | 公网 IP |
|---|---|---|---|---|---|
| VPS `snet.uizhi.eu.org` | **未探测**（无 SSH 凭据） | 未知 | 未知 | 未知 | `66.187.6.46`（DNS） |
| 外网服务器 A | **缺失地址/凭据** | 未知 | 未知 | 未知 | 未知 |
| 外网服务器 B | **缺失地址/凭据** | 未知 | 未知 | 未知 | 未知 |

### VPS 公网只读证据

```text
DNS:  snet.uizhi.eu.org -> 66.187.6.46
GET https://snet.uizhi.eu.org:8090/healthz
HTTP/2 200
x-snet-server-version: 1.3.0
{"apiVersion":1,"buildDate":"2026-10-27T01:48:22Z","commit":"8359cb7","serverVersion":"1.3.0","status":"ok"}
```

- 8090 是 HTTPS，HSTS 已开启。
- 证书：`CN=snet.uizhi.eu.org`，Let's Encrypt（issuer `YE1`），有效期 `2026-10-16` 至 `2027-01-14`。
- `8094/tcp` 从外部请求超时；可能绑定 127.0.0.1 或被防火墙拦截，不能据此判断旧 relay 状态。
- `8092/udp` 无法从当前环境只读判断；需要 VPS 上 `ss`/防火墙检查。

### 当前 opencode 环境（非目标节点）

```text
Linux x86_64, root
/dev/net/tun: 不存在
docker/podman/nerdctl: 不存在
```

这不属于三台部署目标，仅说明本地无法替代真机 TUN 验证。

## 3. VPS 旧部署盘点

### 已确认（公网只读）

- 旧 server 正在 8090/HTTPS 提供服务：版本 `1.3.0`，commit `8359cb7`，buildDate `2026-10-27T01:48:22Z`。
- TLS 证书为 Let's Encrypt，覆盖 `snet.uizhi.eu.org`，有效期到 2027-01-14。
- 旧 server 可能仍在运行；未停止、未清理。

### 未确认（需 SSH）

以下命令**未执行**，因此不能给出实际清单：

```bash
systemctl list-units --type=service | grep -i snet
systemctl status snet-server --no-pager
ls -la /usr/local/snet/ /etc/snet/
find / -name 'snet.db'
grep -r 'snet.uizhi' /etc/nginx /etc/caddy /etc/systemd
/usr/local/snet/bin/server -version
```

按 atlas/历史部署文档的**预期**位置（不是已验证事实）：

```text
/usr/local/snet/bin/server
/usr/local/snet/backups/
/etc/snet/
/etc/letsencrypt/live/snet.uizhi.eu.org/
```

### 清理 vs 保留建议

| 对象 | 建议 |
|---|---|
| TLS 证书/私钥（Let's Encrypt） | **保留/备份**；全新 server 可复用同一证书，客户端系统信任即可 |
| SSH 配置/authorized_keys/密钥 | **保留**；不要随旧应用清理 |
| 旧 bbolt `snet.db` | **先备份再清理**；位置需 SSH 确认，建议复制到 `/root/snet-old-backup-YYYYMMDD/` |
| `/etc/snet/` 旧配置/env | 备份后清理；不要带走旧 token/secret 到新部署 |
| `/usr/local/snet/bin` 旧二进制 | 可清理/替换；保留备份以便回滚 |
| 旧 systemd units | 停用/删除前先记录 unit 文件与 env；本次不执行 |
| 防火墙规则 | 保留 SSH/80/443；确认 UDP 8092 新 relay 放行 |

**不能确认的项必须标阻塞**：旧 bbolt 实际路径、TLS 文件实际路径、旧 relay 是否占用 8092、旧 systemd unit 名称与 env。

## 4. 客户端硬编码与 TLS

### 硬编码地址

```text
internal/constants/constants.go: DefaultServer = "https://snet.uizhi.eu.org:8090"
shared/web/types.ts: FIXED_SERVER = "https://snet.uizhi.eu.org:8090"
```

结论：客户端硬编码地址与 VPS 完全匹配；全新部署在 VPS 上不需要 `/etc/hosts` 或本地 TLS hack。`snetctl`/ctl 的 `enforceServer` 也拒绝其他地址。

### 客户端 TLS 校验

`internal/client/control.go` 使用系统信任库，不读取用户配置的 CA 路径，也没有跳过校验的开关。

当前 VPS 是 Let's Encrypt 公共证书，客户端默认系统信任即可。全新部署建议：

1. 复用现有 Let's Encrypt 证书（最小改动，需 SSH 确认路径）；
2. 或重新用 certbot 签发同名证书；
3. 自签证书不应作为生产方案；客户端不会跳过校验。

## 5. Relay v2 启用方案

### 部署点

推荐 **与 VPS server 同机**：

```text
snet-server   :8090/tcp HTTPS, :8091/udp probe
snet-relay    :8092/udp Relay v2, :8094/tcp metrics (bind 127.0.0.1)
```

也可独立第三台，但需：

- 公网 UDP 8092 可达；
- server 的 `SNET_RELAY_V2_ENDPOINTS` 指向该主机 `host:8092`；
- relay 与 server 使用同一个 `SNET_RELAY_SECRET`。

### 环境变量清单

| 进程 | 变量/参数 | 说明 |
|---|---|---|
| relay | `SNET_RELAY_SECRET` | 必填，>=16 bytes；建议 32 bytes hex |
| relay | `SNET_RELAY_SECRET_OLD` | 可选，轮换用 |
| relay | `-addr` / 默认 `:8092` | UDP 监听；生产建议 `0.0.0.0:8092` 或明确公网地址 |
| relay | `-metrics-addr` / 默认 `:8094` | **默认监听所有接口；生产建议 `127.0.0.1:8094`** |
| relay | `-session-ttl` / 默认 `30s` | 空闲 session TTL |
| relay | `-max-sessions` / 默认 `4096` | 并发 session 上限 |
| server | `SNET_RELAY_V2_ENDPOINTS` | 逗号分隔，例如 `snet.uizhi.eu.org:8092`；首个为主 |
| server | `SNET_RELAY_V2_ENDPOINT` | 单 endpoint 旧形式；被 `ENDPOINTS` 覆盖 |
| server | `SNET_RELAY_SECRET` | 必须与 relay 相同 |
| server | `SNET_RELAY_SECRET_OLD` | 可选，轮换用 |

服务端逻辑：

- `cmd/server/main.go` 读 `SNET_RELAY_V2_ENDPOINTS`/`SNET_RELAY_V2_ENDPOINT`、`SNET_RELAY_SECRET`。
- 有 endpoint 且 secret 非空才 `SetRelayV2Endpoints`；否则 Relay v2 保持 disabled。
- ticket 由 `internal/relay/ticket.go` 的 HMAC-SHA256 签发，TTL 1h；relay 用同一 secret 校验。
- 客户端从 `/peers` 的 `RelayV2` 看到 endpoint，从 `/api/v1/networks/{nid}/relay-ticket` 取 ticket；PathManager 使用 `RelayTypeData` 转发。

### Secret 管理

生成（不打印值）：

```bash
umask 077
openssl rand -hex 32 > /etc/snet/relay.secret
```

保存：

- `/etc/snet/relay.secret`，root:root，0600；
- 或 systemd `EnvironmentFile=/etc/snet/relay.env`，0600；
- 不写入 git、不打印、不放进 Web/日志；
- 备份到受控位置；轮换时用 `SNET_RELAY_SECRET_OLD` 过渡。

### 部署顺序（全新项目，无 flag day）

1. 先启动 relay，验证 8092/udp 监听和 `127.0.0.1:8094/healthz`。
2. server 配 `SNET_RELAY_V2_ENDPOINTS` + 同一 `SNET_RELAY_SECRET`，重启。
3. 检查 `/healthz` 正常；`/peers` 或 `/ctl/status` 显示 Relay v2 enabled/endpoint。
4. 再启动两台 snetd，绑定/建网/入网。
5. 客户端 PathManager 进入 relay-v2；验证直连升级与 relay 回退。

### MTU / 网络约束

- `protocol.DefaultMTU = 1320`；snetd 隧道使用 1320。
- relay 只转发 opaque WireGuard 包，不改 MTU；链路需允许 1320 以内 UDP。
- 防火墙放行 UDP 8092；metrics 8094 仅本机。
- Relay v2 与旧 relay 端口池 51820-52075 不冲突；snetd WG 52100+ 也不冲突。

## 6. 全新部署步骤草案（未执行）

1. **备份与清理旧 VPS**
   - SSH 后记录 systemd units、env、TLS 路径、bbolt 路径。
   - 备份旧 `snet.db`、`/etc/snet`、TLS 证书/私钥、unit 文件到受控目录。
   - 停用旧 `snet-server`/旧 relay；保留 SSH、80/443、证书和防火墙。
   - 清理旧二进制/旧配置前确认备份可读。

2. **交叉编译**
   - `GOOS=linux GOARCH=amd64 go build -o server ./cmd/server`
   - `GOOS=linux GOARCH=amd64 go build -o relay ./cmd/relay`
   - `GOOS=linux GOARCH=amd64 go build -o snetd ./cmd/client/snetd`
   - `GOOS=linux GOARCH=amd64 go build -o snetctl ./cmd/client/snetctl`
   - `VERSION` 缺失不影响运行；发布版本一致性仍需上游补。

3. **VPS 部署 server + relay**
   - 安装新二进制、systemd units、`/etc/snet/relay.env`（0600）。
   - relay 先起，metrics 绑 127.0.0.1:8094。
   - server 配 TLS 证书 + Relay v2 env，重启。
   - 验证 8090 HTTPS、8091 UDP probe、8092 UDP relay、8094 本机 metrics。

4. **两台外网服务器部署 snetd**
   - 每台需要 root/`CAP_NET_ADMIN` + `/dev/net/tun`。
   - 独立 config、device-id、ctl-token、ctl 端口；WG 端口 52100/52101。
   - 通过 `snetctl` 或 Web 绑定设备授权码（若启用 `RequireDeviceAuth`）。
   - 一台建网，另一台 join；检查 `/ctl/status`、`/ctl/peers`、`relayPath.peerDirect`。

5. **验证**
   - overlay ping/HTTP/iperf3；直连/relay 回退；Relay v2 ticket；重启恢复。
   - 再做真实 NAT/IPv6/多 relay 真机矩阵；不要把单机结果写成真机已验证。

6. **Web/Docker（后续）**
   - 当前快照缺 Dockerfile/nginx/entrypoint/compose 与 Web 产物；见 `NETWORK-VALIDATION-PLAN.md`。
   - 在补齐部署文件前，先用 `snetctl` 验证组网。

## 7. 阻塞与下一步

### 能否部署

- VPS 公网可达、TLS 正常、客户端硬编码匹配；**代码层面具备全新部署条件**。
- 但三台 SSH 未提供，无法确认架构/TUN/端口/旧部署；不能判定“三节点可部署”。

### Relay v2 还缺什么

1. 确认 relay 部署点与公网 UDP 8092 可达。
2. 生成并安全保存 `SNET_RELAY_SECRET`（不打印）。
3. 配置 relay + server env 并重启。
4. 防火墙放行 8092/udp；metrics 绑 127.0.0.1。
5. 两台 snetd 的 TUN/root/设备授权。

### 最大阻塞

**缺少三台 SSH 地址、用户名、认证方式/密钥和 sudo 信息**；其次是两台外网服务器的 TUN/root/防火墙未知，以及 VPS 旧部署的 systemd/bbolt/TLS 路径未探测。

## 8. 只读探测证据汇总

```text
VPS DNS: 66.187.6.46
VPS 8090: HTTP/2 200, serverVersion 1.3.0, commit 8359cb7
VPS TLS: Let's Encrypt, CN/SAN snet.uizhi.eu.org, expires 2027-01-14
VPS 8094/tcp: 外部超时（可能 loopback/firewall，未确认）
本地 relay 构建: go build ./cmd/relay 成功；-h 显示 -addr/-metrics-addr/-secret/-secret-old/-session-ttl/-max-sessions
```

未执行：SSH 登录、systemctl、文件清理、服务停止/启动、容器启动、snetd/server/relay 常驻进程。
