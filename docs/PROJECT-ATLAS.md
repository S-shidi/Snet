# SNET 项目全局图谱（Agent Atlas）

> 跨会话的项目记忆索引，不是完整设计文档。
> 读取顺序：本文件 → 匹配的领域 skill（若存在）→ 代码/测试。
> 维护规则见 §8。

## 0. 新鲜度与真相源

```text
last_verified:        2026-11-21
verified_code_commit: adcef6c
version:              1.2.1
verified_worktree:    含用户未提交改动（desktop-windows/main.go、shared/web/ui.ts、styles.css）
state:                SnetServ / admin-server 拆分 + 每网络 SnetServ 绑定 + Wails v3 桌面
                      + Android 单网络 + Docker/NAS；Go 测试为当前唯一回归基线
verified_with:        2026-11-21 go build ./...；go test ./...；go vet ./...；tsc --noEmit
```

真相源优先级：

1. 代码与测试（`cmd/`、`internal/`、`shared/web/`、`snetbind/`）
2. `DESIGN.md`（系统设计、数据模型、端口、API）
3. `README.md`（安装、构建、部署入口）
4. 本 atlas（全局索引与不变量）
5. 历史文档（`docs/archive/`，只作背景，不作实现依据）

遇到冲突：先验证代码，再修文档。

## 1. 一句话与边界

SNET 是面向个人/家庭/小团队的 WireGuard 虚拟组网工具。Go 服务端只做协调、探测、
票据和中继；真正的数据面在 `snetd` 的 WireGuard 隧道里。

核心边界：

- 服务端不转发业务数据，除非客户端选择 relay fallback。
- 每个网络绑定一个 SnetServ；客户端按网络路由请求，不假设全局唯一服务器。
- 数据面端到端由 WireGuard 加密；协调面不接触业务明文。
- Android 为单网络模式；桌面端支持多网络并行。
- 平台管理走独立的 `admin-server` + 内部 Admin RPC；公网 API 不暴露 `/admin`。

## 2. 组件地图

| 组件 | 入口 | 职责 | 权威文件 |
|---|---|---|---|
| SnetServ | `cmd/snetserv` | 公网 API `:8090`、probe `:8091`、Relay v2 票据、可选 legacy relay、内部 Admin RPC `:8096` | `internal/server/` |
| Admin Server | `cmd/admin-server` | 管理台 `:8095`、登录 session、转发内部 Admin RPC | `internal/adminclient/`、`internal/adminui/` |
| Relay v2 | `cmd/relay` | 无状态 UDP session relay `:8092`、metrics `:8094` | `internal/relay/` |
| 客户端守护进程 | `cmd/client/snetd` | 隧道、peer 轮询、NAT、路径管理、密钥轮换、`/ctl/*` | `internal/client/` |
| 客户端 CLI | `cmd/client/snetctl` | 调用本地 `/ctl/*` | `cmd/client/snetctl/` |
| 共享 Web UI | `shared/web` | 桌面/Docker/Android WebView 唯一 UI | `ui.ts`、`types.ts`、`subnet.ts` |
| Android | `android/app-native` + `snetbind` | Kotlin + WebView + gomobile（4 个导出方法） | `SnetVpnService.kt`、`snetcore.go` |
| macOS | `desktop-mac-v3` | Wails v3 + LaunchDaemon + ctlProxy | `main.go`、`service.go` |
| Windows | `desktop-windows` | Wails v3 + Wintun + SNetDaemon 服务 | `main.go`、`service.go` |
| 生产部署 | `deploy-prod` | Docker 客户端/服务端、手写 systemd 客户端 | `docker-*/`、`manual-client/` |
| 历史归档 | `archive/`、`docs/archive/` | 旧 Tauri 客户端、快照期文档；不参与构建 | `archive/README.md` |

## 3. 目录地图

```text
cmd/                 Go 入口：snetserv、admin-server、server、relay、snetd、snetctl
internal/            Go 实现：server、relay、client、protocol、adminclient、adminui、buildinfo
shared/web/          共享 Web UI（桌面、Docker、Android WebView）
snetbind/            Android gomobile 绑定
android/app-native/  Android 客户端
desktop-mac-v3/      macOS Wails v3
desktop-windows/     Windows Wails v3
deploy-prod/         生产 Docker / systemd 部署
deploy/              传统部署脚本与运维文档
scripts/             构建、发布、安装、E2E、硬件
docs/                当前文档 + archive/ 历史文档
archive/             历史客户端与产物
```

## 4. 三条主流程

### 4.1 建网、邀请与审批

```text
创建网络 → 自动/指定网段 → pairingCode（72h/5 次）
  → snet://join?nid&code[&name][&s=加密服务器]
  → 客户端解析链接，按网络自己的 SnetServ 加入
  → 免审批直接分配；审批制进入 pending
  → owner approve → pending claim → 节点 token → peers
```

- `Network` 记录 `snetServUrl` / `snetServId`；不再有可配置全局默认服务器。
- 链接的 `s=` 是加密服务器地址；无 `s=` 回退官方默认。
- 每设备每 SnetServ 只能创建一个自有网络。
- `managed` 网络无网主、不参与僵尸清理；普通网络 72h 无在线删除。

### 4.2 连接、直连升级与中继回退

```text
每 15s UDP probe :8091 → 观测公网 IP:port
  → 候选优先级 LAN > IPv6 > 公网打洞
  → 打洞端口 ±1..±8（17 候选，并行 5 个/批）
  → Relay v2：先建 relay session，直连 5s ping；15s 无包回退
  → 已建立低优先级路径时每 10s 继续探测更高候选
```

- Relay v2：1h HMAC ticket，HELLO 建 session，DATA 按 `recipientID` 精确转发；MTU 1320。
- 每个 peer 用 `127.0.0.1:Pi` loopback shim，WG endpoint 不随路径变化。
- 多 relay：每实例维持 session，发送优先第一个 ready；失败切其他实例。
- NAT 映射：UPnP-IGD / NAT-PMP / PCP，默认开启，5m 租约、2m 续期；WAN IP 必须等于观测公网 IP 才广播。
- 同公网出口 sibling 禁用公共 IP hairpin，固定 relay。
- 看门狗每 10s 采样；relay 路径连续 3 次无流量且握手过期时强制重选中继。
- 未启用 Relay v2 时保留 legacy direct-first：20s 未握手回退，180s 后重试直连。

### 4.3 子网路由、Hub 与 WireGuard 导出

```text
owner 委派 CIDR → 目标节点 allowRelay + 网关配置成功
  → route pending → active
  → 成员安装 AllowedIPs / 系统路由
  → 访问目标节点背后的物理内网
```

- 成员视角保留 `AllowedSubnets`（数据面需要），只清空 `Role`。
- 官方 Hub 由服务端配置，优先分配 `.254`，作为公网出口。
- 导出的 WireGuard 私钥只保存在本机；不上传协调服务器。

## 5. 不可违反的全局不变量

1. **每网络 SnetServ**：网络请求必须使用该网络的 `SnetServURL`；不要恢复全局服务器假设。
2. **端口规划**：`8090` HTTPS、`8091` UDP probe、`8092/udp` Relay v2、`8094` metrics、
   `51820-52075` legacy relay、`52100+` 成员 Docker WG、`19432` 本地 ctl、
   `8095` admin-server、`8096` 内部 Admin RPC。
3. **锁规则**：`Daemon.mu` / `Store.mu` 保护状态；持锁禁止网络 I/O、文件写入、进程 spawn；
   持 `d.mu` 时用 `hostnameLocked()` 而不是 `hostname()`。
4. **持久化**：新增需要重启保留的字段必须同时更新内存与 bbolt `persist*`。
5. **API 版本**：不兼容 wire 变更提升 `protocol.APIVersion`；主版本不匹配返回 426；
   `/healthz` 的 `apiVersion` 同步。
6. **鉴权**：`/ctl/*` 用 `ctl-token`；Docker Web 额外要求 Web session（`X-Snet-Web: 1`）；
   `admin-server` session 与内部服务令牌分离；日志经 `sanitizeLog`；绝不把 token、授权码、私钥写进仓库/日志/聊天。
7. **角色**：只有 `owner` / `member`；legacy `admin` 角色拒绝；owner 转让后旧 owner 降为 member。
8. **bbolt bucket**：9 个：`networks`、`nodes`、`tokens`、`devices`、`pending`、`admin`、`authcodes`、`relaytopo`、`meta`（`meta` 存 `serverId`）。
9. **Web 构建**：改 `shared/web` 后必须重新构建；不要手改 `deploy/docker/web/` 产物。
10. **平台文件**：`internal/client` 的平台文件必须成组补齐，否则交叉编译失败。
11. **Android 单网络**：加入/创建新网络会断开当前网络；桌面端才支持多网络并行。

## 6. 存储、API 与代码入口

- 入口：`cmd/snetserv/`、`cmd/admin-server/`、`cmd/relay/`、
  `cmd/client/snetd/`、`cmd/client/snetctl/`。
- 服务端路由：`internal/server/server.go`（约 34 个 `/api/v1/*` + 27 个 legacy `/admin/*`）。
- 内部 Admin RPC：`internal/server/admin_rpc.go`（loopback 或私有 CIDR + 服务令牌）。
- 客户端 `/ctl`：`internal/client/ctl.go`（约 39 条注册）。
- 服务端存储：`internal/server/store.go`；legacy relay：`internal/server/relay.go`；
  probe：`internal/server/probe.go`；Hub/WireGuard 导出：`internal/server/hub.go`、`wgconfig.go`。
- 客户端：`daemon.go`、`control.go`、`pathmanager.go`、`natmgr.go`、`relaygateway_*.go`、`config.go`。
- 协议：`internal/protocol/types.go`、`link.go`、`relay.go`。
- 管理台：`internal/adminui/admin.html` + `internal/adminclient/client.go`。
- Web 构建：`deploy/docker/web-build/`；输出到 Docker 镜像构建阶段，不提交产物。

## 7. 构建、测试与验证矩阵

| 目标 | 命令 | 说明 |
|---|---|---|
| 全量构建 | `go build ./...` | 当前基线 |
| 全量测试 | `go test ./...` | 当前主要回归手段 |
| 竞态 | `CGO_ENABLED=1 go test -race ./...` | 需要 C 编译器 |
| 静态检查 | `go vet ./...` | 无输出即通过 |
| Web 类型检查 | `cd shared/web && npx --yes -p typescript@5.9.3 tsc --noEmit -p tsconfig.json` | CI 同款 |
| Web 资产 | `./scripts/build-web.sh` 或 `./desktop-windows/frontend/build-frontend.sh` | 改 `shared/web` 后必须执行 |
| 发布产物 | `./scripts/build-release.sh` | 输出 `dist/`，含 server/relay/snetd/snetctl/snetserv/admin-server |
| 交叉编译 | `GOOS=darwin GOARCH=arm64 go build ./...`；`GOOS=windows GOARCH=amd64 go build ./...` | 验证平台文件齐全 |

## 8. 记忆维护协议

每次任务开始：

1. 读本文件 + 一个匹配的领域文档（`DESIGN.md` 或 `docs/`）。
2. 用 `git status --porcelain` 确认工作区状态；不要把用户未提交改动当基线。
3. 广度搜索用 `explore` 子代理；主上下文只保留决策和结论。

每次任务结束：

1. 结构变化（新包/路由/端口/bucket/配置字段/平台契约/构建步骤）→ 更新本文件与 `DESIGN.md`。
2. 更新 §0 的 `last_verified`、`verified_code_commit`、`state`。
3. 至少在 §10 追加一行变更记录。
4. 跑一个相关验证命令（`go test ./...` 或 `tsc --noEmit`）。
5. 不确定的事实标“待验证”，不要写成结论。

## 9. 当前风险与 WIP

- **Relay v2 是 flag day**：服务端一旦下发 `RelayV2.Enabled=true`，新客户端只走 Relay v2；
  上线顺序必须是“全部客户端升级 → 服务端设置 `SNET_RELAY_V2_ENDPOINT(S)`”，回滚先清空服务端变量。
- **admin-server 单服务端**：v1.2.1 只管理一个 SnetServ；多服务端联邦计划 v1.3。
- **authcodes 未挂载**：bucket 与 `BindDevice` 代码保留，但当前没有公开 HTTP 路由；不要当成可用功能。
- **社区共享未闭环**：`description` / `tags` / `visibility` 已存储展示，公开目录发现未落地。
- **NAT 类型是观测值**：不能用于放行或安全决策。
- **Docker/NAS 临时端口**：端口映射会让对端看到 NAT 源端口；此类节点以 relay 为准，
  可用 `SNET_LOCAL_IP` 上报宿主地址并保留运行时端口。
- **文档漂移**：历史评审/快照文档已移入 `docs/archive/`；不要用它们判断当前行为。

## 10. 文档索引

当前文档：

| 文档 | 内容 |
|---|---|
| `README.md` | 安装、构建、部署入口 |
| `DESIGN.md` | 系统设计、数据模型、API、端口 |
| `docs/README.md` | 文档总索引 |
| `docs/DEPLOYMENTS.md` | 生产拓扑与环境 |
| `docs/DECISION-LOG.md` | 已落地决策 |
| `docs/NETWORK-TROUBLESHOOTING.md` | 网络排障 |
| `docs/RELAY-V2-CUTOVER.md` | Relay v2 切换 runbook |
| `docs/ONLINE-STATUS-LOGIC.md` | 在线状态判定 |
| `docs/MAC-DEV-NOTES.md` | macOS 开发与安装 |
| `docs/releases/v1.2.1.md` | v1.2.1 发布说明 |
| `docs/releases/v1.2.0.md` | v1.2.0 历史发布说明 |

历史文档：`docs/archive/`（快照期评审、部署探测、验证计划、relay 决策）；
`deploy/archive/`（旧 NAS/Docker/排障文档）。
历史客户端：`archive/desktop-mac-tauri/`。

## 11. 变更日志

| 日期 | 变更 | 验证 |
|---|---|---|
| 2026-11-21 | 退役 `cmd/server`：主服务端统一 `cmd/snetserv`，管理台独立 `cmd/admin-server`；Compose 增加 8095/内部 RPC，公网 `:8090/admin` 返回 404 | `go build ./...`、`go test ./...`、VPS 8090/8095/8096 验证 |
| 2026-11-21 | Phase 0 联邦化地基：SnetServ 持久化 `serverId`（`meta` bucket）、网络回填 `snetServId`、新增 `/internal/admin/health`、客户端 `/ctl/status` 暴露 `snetServId` | `go build ./...`、`go test ./...`、`tsc --noEmit` |
| 2026-11-21 | 文档与目录整理：重写 `DESIGN.md` / `PROJECT-ATLAS.md`；新增 `docs/README.md`、`archive/README.md`、`docs/archive/`；归档旧 Tauri 客户端与快照文档；更新发布脚本与 macOS CI 缓存路径 | `go build ./...`、`go test ./...`、`go vet ./...`、`tsc --noEmit` |
| 2026-11-08 | 四条线闭环收口：relay T12、NAT P0/P1、pathmanager #1-#10、server 鉴权/路由矩阵/probe | `go test ./... -count=1`、`go test -race ./...`、`go vet ./...` |
| 2026-10-27 | v1.3.0 预备（Relay v2 补齐）与 v1.2.0 发布部署 | `go test ./...`、`go vet ./...`、VPS `/healthz` |
| 2026-11-21 | v1.2.1：多网络端口/启动/托盘收尾、管理后台邀请链接修复、全局默认协调服务清理 | `go test ./...`、`go vet ./...`、Windows/macOS 构建、ACR 镜像发布 |
