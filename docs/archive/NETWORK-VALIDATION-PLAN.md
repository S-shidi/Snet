# Docker Web 多节点组网验证计划

> 状态：只读盘点 + 前置条件计划，未启动 Docker、snetd、server 或 relay。
> 范围决策：2026-11-08 放弃 Android 客户端；Docker Web 是唯一客户端 UI，用于组网验证。
> 目标：先在本机跑通 2 节点，再扩到 3 节点；单机无法验证的 NAT/IPv6/真实 LAN 场景标“需真机”。

## 1. 前置条件清单

图例：**已有** / **需补** / **需上游** / **阻塞**

| # | 前置条件 | 当前事实 | 状态 |
|---|---|---|---|
| 1 | snetd 独立运行 | `cmd/client/snetd/main.go` 支持 `-ctl`、`-config`、`-device-id-file`、`-version`；无 `-server` / `-network` 参数 | 已有 |
| 2 | snetd 首次启动 | `LoadConfigAt` 在 config 不存在时返回默认配置；`Start` 自动生成 device ID/keypair；ctl-token 由 `LoadOrCreateCtlToken` 自动生成在 config 同目录 | 已有 |
| 3 | snetd 运行权限 | 需要 root 或 `CAP_NET_ADMIN` + `/dev/net/tun` 才能建 TUN；当前环境 **无 `/dev/net/tun`** | **阻塞（当前环境）** |
| 4 | 版本文件 | snetd/server 运行不依赖 `VERSION`；`buildinfo.Version` 默认 `1.3.0`；`VERSION` 只影响发布/版本一致性 | 已有（运行）/需上游（发布） |
| 5 | 设备授权码 | 服务器 `RequireDeviceAuth=true` 时需要 admin 生成的授权码；本地验证可先关闭该门禁 | 可选 |
| 6 | Web 构建脚本 | `scripts/build-web.sh` 写 `deploy/docker/web/`，该目录不存在；`esbuild` 不在 PATH | **需补** |
| 7 | Web 依赖 | `shared/web/node_modules` 不存在；`deploy/docker/web-build/node_modules` 只有 `qrcode-generator`；`shared/web/utils.ts` 从 `shared/web` 解析 `qrcode-generator`，当前解析链未验证 | **需补/需验证** |
| 8 | Web 适配器 | `deploy/docker/web-build/web.ts` 仍从 `../../../shared/web/{types,ui}.js` 导入；文件存在 | 已有 |
| 9 | Docker 部署文件 | `deploy/docker/` 下只有 `web-build/`；缺 `Dockerfile`、`nginx.conf`、`entrypoint.sh`、`docker-compose.yml`、`web/` 产物 | **需上游** |
| 10 | Docker 环境 | 当前 `docker` / `docker compose` / `podman` / `nerdctl` 均未安装；`docker ps` exit 127 | **阻塞（当前环境）** |
| 11 | 本地协调服务器 | 可 `go build ./cmd/server`；但客户端 `enforceServer` 只接受硬编码 `https://snet.uizhi.eu.org:8090`，不接受任意 `--server` | **需 workaround 或上游决策** |
| 12 | 本地 relay | `cmd/relay` 可独立启动，需 `-secret`；server 需 `-relay-v2-endpoints` + 同一 secret | 已有代码/需配置 |
| 13 | 多节点端口 | server HTTP 8090、probe 8091、Relay v2 8092/metrics 8094；snetd WG 52100+；旧 relay 51820-52075 | 已有约定 |
| 14 | 浏览器 Web | 需要 nginx 注入 `X-Ctl-Token` 和 `X-Snet-Web: 1`，并代理 `/ctl/*` 到 snetd；当前无 nginx 配置 | **需上游/需补** |

### 1.1 单机启动 snetd 的最小步骤（命令级，未执行）

```bash
go build -o /tmp/snetd ./cmd/client/snetd
mkdir -p /tmp/snet-n1

/tmp/snetd \
  -config /tmp/snet-n1/config.json \
  -ctl 127.0.0.1:19432 \
  -device-id-file /tmp/snet-n1/device.id
```

说明：

- config 不存在时使用默认配置；首次启动会生成 device ID 和 keypair。
- ctl-token 自动写到 `/tmp/snet-n1/ctl-token`。
- 非 root 或无 `/dev/net/tun` 时 daemon 可启动，但隧道网络会持续 retry，不能验证数据面。
- 绑定/加入通过 `snetctl` 或 Web `/ctl/*` 完成；`snetctl` 需用 `SNET_CTL_TOKEN_FILE=/tmp/snet-n1/ctl-token` 指向该节点的 ctl-token。
- `snetd` 没有 `-server` / `-network`；服务器地址由 ctl 的 `create/join/bind` 决定，但 `enforceServer` 只接受固定地址。

### 1.2 Web 构建需要补什么

- `scripts/build-web.sh` 当前要求 `esbuild` 在 PATH；环境未安装。
- 输出目录 `deploy/docker/web/` 不存在；最小改动是 `mkdir -p deploy/docker/web`（不改变脚本路径），但构建未验证。
- `shared/web` 没有 `node_modules`；`qrcode-generator` 只在 `deploy/docker/web-build/node_modules`。需要在 `shared/web` 安装依赖，或设置 `NODE_PATH`/symlink，否则 `shared/web/utils.ts` 的 import 解析可能失败。
- `deploy/docker/web-build/web.ts` 的 shared 导入路径存在，适配器本身无需改。
- 结论：**Web 产物当前不可直接构建**；需补 esbuild + 依赖解析 + 输出目录。

### 1.3 Docker 部署最小文件清单

`docker compose up` 至少需要：

```text
deploy/docker/Dockerfile
deploy/docker/nginx.conf
deploy/docker/entrypoint.sh
deploy/docker/docker-compose.yml
deploy/docker/web/index.html
deploy/docker/web/styles.css
deploy/docker/web/app.js
```

当前只有 `deploy/docker/web-build/`。`deploy/README.md` 有 compose 模板和运行参数，但 **Dockerfile / nginx.conf / entrypoint.sh 的确切内容不在仓库**；atlas 提到历史 NAS runtime tar 内含 nginx 配置，但 tar 也不在快照。不能可靠反推，标 **需上游**。compose 文件可参考 `deploy/README.md` 模板起草，但仍需 Dockerfile/nginx/entrypoint 才能启动。

## 2. 多节点拓扑

### 2.1 推荐：本机 2 节点

```text
Host
├── snet-server  :8090 HTTP, :8091 UDP probe
├── snet-relay   :8092 UDP, :8094 metrics
├── snetd-1      ctl :19432, WG :52100, config /tmp/snet-n1
└── snetd-2      ctl :19433, WG :52101, config /tmp/snet-n2
```

- 每个 snetd 必须有独立 config 路径、device-id 文件、ctl-token、ctl 端口、WG 端口。
- 两个节点不能共用 `/dev/net/tun` 对应的同一 netns；Docker 下应使用独立 netns（bridge 或独立容器），不要简单用两个 host-network 容器共享同一 host netns。
- 旧 relay 池 51820-52075 与 snetd WG 52100+ 不冲突；Relay v2 8092 也不冲突。
- **不要连生产 VPS**：会污染生产网络。应本地起 server + relay。
- 但客户端固定服务器地址是硬约束：本地 server 需要 `/etc/hosts` 将 `snet.uizhi.eu.org` 指向 127.0.0.1，并给本地 server 配置该 hostname 的**系统信任** TLS 证书（或把本地 CA 装入系统信任库），或由上游放宽 `enforceServer`；客户端已不再支持 `--ca-path`。这是当前单机方案的关键 workaround，未验证。

### 2.2 3 节点

在 2 节点基础上增加：

```text
snetd-3  ctl :19434, WG :52102, config /tmp/snet-n3
```

- 3 个节点用于 T2/T6/T10/T12 等多路径/多网络场景。
- 若要验证真实 LAN/public/NAT 差异，单机只能模拟“同 host/同出口”，不能替代真机。

## 3. 验证矩阵

对照 `NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md` §7 T1-T14。

| ID | 场景 | 操作 | 通过标准 | 单机 Docker 可验证？ | 需真机？ |
|---|---|---|---|---|---|
| T1 | 同 LAN 互通 | 2 节点 ping + HTTP | direct，<20ms，0% loss | 部分（同 host/bridge 可模拟；真实 LAN 需真机） | 是（真实 LAN） |
| T2 | 同公网出口 cross-sibling | 2-3 节点两两 ping | 精确投递，0% loss，不复制给无关 sibling | 部分（同出口可模拟；需 tcpdump/relay 日志） | 是（真实共享出口） |
| T3 | 不同公网 cone NAT | A↔B ping + iperf3 | direct，0% loss，RTT 符合物理路径 | 否 | 是 |
| T4 | 对称 NAT/CGNAT | 节点互 ping | relay，0% loss，允许 direct 失败 | 否 | 是 |
| T5 | IPv6 可用 | 双栈节点互 ping | 优先 IPv6 direct，0% loss | 取决于宿主 IPv6；当前环境未验证 | 是 |
| T6 | 一侧 direct、一侧 relay | 强制一侧 direct、一侧 relay | 不得黑洞；收敛到同一模式或 relay shim 可投递 | 部分（可用防火墙/端口阻断模拟） | 是（真实路径） |
| T7 | relay 端口故障 | block relay 端口 | <30s 切备 relay 或回 direct | 部分（本机 iptables/nftables 可模拟） | 是（真实网络故障） |
| T8 | NAT rebinding | 切换 Wi-Fi/重建映射 | <60s 恢复，旧 flow 淘汰 | 否 | 是 |
| T9 | 客户端后台/息屏 | 定时 ping | 隧道保持或 <60s 恢复 | 否（Docker 进程重启不等价） | 是 |
| T10 | 多网络 10/8 重叠 | 加入两个 10.x 网络 | 不误捕获本机 LAN，不路由冲突 | 部分（多网络配置可模拟；真实 LAN 需真机） | 是（真实 LAN） |
| T11 | 吞吐/MTU | iperf3 TCP/UDP，1400/1420B | 无分片/黑洞，loss <1% | 部分（本机 loopback/bridge 吞吐不代表物理） | 是（真实链路） |
| T12 | relay 放大/重复 | tcpdump 计数 | 每包 ≤1 份有效副本，无错误 sibling 持续收包 | 部分（本机 tcpdump/relay 日志可做） | 是（真实共享出口） |
| T13 | relay 控制面滥用 | 未认证 whoami/group、洪泛 | 有限流/鉴权，不泄露 peer 列表 | 是（本地 relay 可测） | 否 |
| T14 | 凭据/日志 | 检查日志与 `/ctl/status` | 无 token/privateKey/pairing code | 是（本地可测） | 否 |

单机优先可做：**T13、T14**，其次 **T2/T6/T7/T10/T12 的本地子集**；T1/T3/T4/T5/T8/T9/T11 必须真机或真实网络。

## 4. 最小可执行路径（第一个 2 节点互通）

1. **构建并启动本地协调面**
   - 构建 `cmd/server`、`cmd/relay`、`cmd/client/snetd`、`cmd/client/snetctl`。
   - 起本地 server + relay（Relay v2 8092，secret 自定），先关闭 `RequireDeviceAuth` 以便快速创建/加入。
   - 处理固定服务器地址：`/etc/hosts` 映射 `snet.uizhi.eu.org` → 127.0.0.1 + 本地 TLS 证书；否则客户端会拒绝非固定地址。

2. **起两个 snetd 节点**
   - 两个独立 config 目录、ctl 端口 19432/19433、WG 端口 52100/52101。
   - 每个节点需要 `/dev/net/tun` + `CAP_NET_ADMIN`；当前环境没有，必须在有 TUN 的 Linux 主机或正确配置的 Docker 容器中做。
   - 用 `SNET_CTL_TOKEN_FILE` 指向各自 ctl-token，通过 `snetctl` 创建/加入网络。

3. **验证 overlay 互通**
   - 节点 1 创建网络，节点 2 用邀请码加入。
   - 检查 `/ctl/status`、`/ctl/peers`、`peerDirect`/`peerPaths`。
   - 从节点 1 ping 节点 2 的 overlay IP，再跑 HTTP/iperf3（若环境有）。
   - Web UI 作为后续步骤：先补 Dockerfile/nginx/entrypoint/compose 与 Web 产物，再验证浏览器登录 + `/ctl/*`。

## 5. 阻塞项（必须用户/上游提供）

| 阻塞 | 为什么阻塞 | 需要谁提供 |
|---|---|---|
| Docker/容器运行时 | 当前 `docker` 不存在，无法起 compose/容器 | 用户提供 Docker 或等价运行时 |
| `/dev/net/tun` + NET_ADMIN | 当前环境无 `/dev/net/tun`，snetd 不能建隧道 | 用户提供有 TUN 的 Linux 主机或特权容器 |
| Dockerfile / nginx.conf / entrypoint.sh / compose | 仓库只剩 `web-build/`；文档不能可靠反推确切运行配置 | 上游/历史 runtime tar |
| Web 构建依赖 | `esbuild` 缺失，`shared/web/node_modules` 缺失，输出目录缺失 | 用户/环境安装，或上游提供预构建 `app.js` |
| 固定服务器地址 | 客户端只接受 `https://snet.uizhi.eu.org:8090`；本地 server 需 hosts + TLS workaround | 用户决策 workaround，或上游放宽限制 |
| `VERSION` | 运行不阻塞，但发布/版本一致性无法验证 | 上游版本锚点 |
| 生产 VPS | 不应连生产做组网验证 | 用户确认本地 server 方案 |

## 6. 结论

- **当前环境不能直接跑 Docker Web 多节点组网**：没有 Docker，也没有 `/dev/net/tun`。
- 代码层面 snetd/server/relay 可构建，Web 适配器仍在；但 Web 构建与 Docker 部署文件缺失。
- 最小可行路径是：**本地 server + relay + 2 个 snetd（有 TUN 的 Linux 主机）→ snetctl 建网/入网 → overlay ping/HTTP**；Web UI 与 Docker 部署作为后续补齐项。
- 不建议连生产 VPS；本地 server 需要先解决固定地址 + TLS 的 workaround。
