# SNET 工作区快照地图（server + snetd + web core）

> 只读快照说明，不是设计文档。生成于 2026-11-07。
> 根目录：`/workspace/Snet`；基线提交：`3fb8b97`（快照导入）；当前 HEAD：`d124a1f`。
> 上游 `c379790` 未比对；本工作区不是完整项目仓库。

## 1. 目录树与体量

统计命令：`find . -type f -not -path './.git/*' | sort`，行数用 `wc -l` 汇总。

| 顶层 | 文件数 | 行数 | 说明 |
|---|---:|---:|---|
| `(root)` | 6 | 858 | `.dockerignore`、`.gitignore`、`DESIGN.md`、`README.md`、`go.mod`、`go.sum` |
| `cmd/` | 6 | 926 | `server`、`relay`、`client/{snetd,snetctl}` |
| `deploy/` | 51 | 18,355 | 其中 `deploy/docker/web-build/node_modules/` 占 31 个文件；非 node_modules 为 20 个文件 / 3,931 行 |
| `docs/` | 3 | 916 | `PROJECT-ATLAS.md`、`NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md`、`ONLINE-STATUS-LOGIC.md` |
| `internal/` | 61 | 31,394 | 60 个 Go 文件（27,721 行）+ `internal/server/admin.html`（4,599 行） |
| `scripts/` | 9 | 767 | 保留 `build-web.sh`、e2e、NAS/remote 辅助脚本 |
| `shared/` | 8 | 3,008 | `shared/web/` 全部 |
| **合计** | **144** | **56,224** | 不含 `.git/`；包含 `node_modules/` |

顶层文件明细：

```text
  12  .dockerignore
  62  .gitignore
 545  DESIGN.md
 174  README.md
  22  go.mod
  43  go.sum
```

`deploy/` 非 `node_modules` 文件：

```text
deploy/DOCKER-COMPOSE-DEPLOY.md
deploy/NETWORK-TROUBLESHOOTING.md
deploy/README.md
deploy/SERVER-OPTIMIZATION.md
deploy/VPS-DEPLOY-GUIDE.md
deploy/archive/CONNECTION-REPORT.md
deploy/archive/OPTIMIZATION-REPORT.md
deploy/archive/SERVER-DIAGNOSIS.md
deploy/archive/SNOLOGY-MCP-INSTALLED.md
deploy/archive/UPDATE-NAS-MANUAL.md
deploy/archive/UPDATE-NAS.md
deploy/diagnose-server.sh
deploy/docker/web-build/package-lock.json
deploy/docker/web-build/package.json
deploy/docker/web-build/web.ts
deploy/mac-daemon-migrate.sh
deploy/snet-relay.service
deploy/snet-server.service
deploy/update-nas-fixed.sh
deploy/update-nas-synology.sh
```

## 2. Go 包清单

共 **66 个 Go 文件 / 27,721 行**。以下按包列出文件名、行数与 `//go:build` 约束。

### `cmd/client`（4 文件 / 615 行）

```text
 408  cmd/client/snetctl/main.go
  31  cmd/client/snetd/main.go
  34  cmd/client/snetd/main_unix.go        [//go:build !windows]
 142  cmd/client/snetd/main_windows.go     [//go:build windows]
```

### `cmd/relay`（1 文件 / 98 行）

```text
  98  cmd/relay/main.go
```

### `cmd/server`（1 文件 / 213 行）

```text
 213  cmd/server/main.go
```

### `internal/buildinfo`（2 文件 / 50 行）

```text
  25  internal/buildinfo/buildinfo.go
  25  internal/buildinfo/buildinfo_test.go
```

### `internal/client`（30 文件 / 11,406 行）

```text
 207  internal/client/config.go
 394  internal/client/control.go
  46  internal/client/crypto.go
 671  internal/client/ctl.go
  70  internal/client/ctl_auth_test.go
  67  internal/client/ctl_token.go
3577  internal/client/daemon.go
2138  internal/client/daemon_test.go
 107  internal/client/device.go
  11  internal/client/device_android.go           [//go:build android]
  25  internal/client/device_darwin.go            [//go:build darwin]
  23  internal/client/device_linux.go             [//go:build linux && !android]
   9  internal/client/device_other.go             [//go:build !darwin && !linux && !windows]
  90  internal/client/device_test.go
  28  internal/client/device_windows.go           [//go:build windows]
  26  internal/client/gateway_darwin.go           [//go:build darwin]
   9  internal/client/gateway_other.go            [//go:build !linux && !android && !darwin]
  33  internal/client/gateway_proc.go              [//go:build linux || android]
 856  internal/client/natmgr.go
 431  internal/client/natmgr_test.go
  33  internal/client/net_helpers.go
1476  internal/client/pathmanager.go
 419  internal/client/pathmanager_test.go
 361  internal/client/tunnel.go
  58  internal/client/tunnel_android.go           [//go:build android]
  18  internal/client/tunnel_factory_android.go   [//go:build android]
   8  internal/client/tunnel_factory_default.go   [//go:build !android]
  77  internal/client/tunnel_linux.go             [//go:build linux && !android]
  77  internal/client/tunnel_unix.go              [//go:build !windows && !android && !linux]
  61  internal/client/tunnel_windows.go           [//go:build windows]
```

平台文件分组（完整闭合）：

| 维度 | 文件与约束 |
|---|---|
| 设备 ID | `device_android.go`(android) / `device_darwin.go`(darwin) / `device_linux.go`(linux && !android) / `device_windows.go`(windows) / `device_other.go`(!darwin && !linux && !windows) |
| 网关 | `gateway_darwin.go`(darwin) / `gateway_proc.go`(linux \|\| android) / `gateway_other.go`(!linux && !android && !darwin) |
| TUN | `tunnel_android.go`(android) / `tunnel_linux.go`(linux && !android) / `tunnel_windows.go`(windows) / `tunnel_unix.go`(!windows && !android && !linux) |
| TUN 工厂 | `tunnel_factory_android.go`(android) / `tunnel_factory_default.go`(!android) |
| snetd 入口 | `main_unix.go`(!windows) / `main_windows.go`(windows) |

说明：没有单独的 `tunnel_darwin.go`，darwin 走 `tunnel_unix.go`；Android 平台文件保留，但 `snetbind/` 缺失，Android 目标无法端到端构建/验证。

### `internal/constants`（1 文件 / 7 行）

```text
   7  internal/constants/constants.go
```

### `internal/protocol`（4 文件 / 741 行）

```text
  50  internal/protocol/link.go
 124  internal/protocol/relay.go
  72  internal/protocol/relay_test.go
 495  internal/protocol/types.go
```

### `internal/relay`（4 文件 / 1,132 行）

```text
 551  internal/relay/server.go
 334  internal/relay/server_test.go
 177  internal/relay/ticket.go
  70  internal/relay/ticket_test.go
```

### `internal/server`（19 文件 / 13,459 行，含 `admin.html` 4,599 行）

```text
 629  internal/server/admin_test.go
 307  internal/server/authcode_expiry_test.go
 949  internal/server/authcode_test.go
 295  internal/server/hardening_test.go
  90  internal/server/ipv6_test.go
 489  internal/server/owner_test.go
 144  internal/server/pagination_test.go
 240  internal/server/pending_test.go
 228  internal/server/persist_test.go
  58  internal/server/probe.go
 581  internal/server/relay.go
1775  internal/server/relay_test.go
 128  internal/server/relay_v2_test.go
1334  internal/server/server.go
 322  internal/server/server_test.go
 287  internal/server/share_test.go
5268  internal/server/store.go
 169  internal/server/testhelpers_test.go
 166  internal/server/version_test.go
```

## 3. Web 源文件

`shared/web/` 实际为 **8 个文件 / 3,008 行**（不是 9 个）：

```text
 189  shared/web/index.html
   8  shared/web/qrcode-generator.d.ts
 645  shared/web/styles.css
 182  shared/web/subnet.ts
  14  shared/web/tsconfig.json
 122  shared/web/types.ts
1623  shared/web/ui.ts
 225  shared/web/utils.ts
```

说明：若把 Docker 侧适配器 `deploy/docker/web-build/web.ts`（280 行）也算作 Web 源，则相关源文件为 9 个，但它不在 `shared/web/` 下。

## 4. 契约位置

| 契约 | 位置 |
|---|---|
| `/ctl/*` 路由 | `internal/client/ctl.go`，首条 `mux.HandleFunc` 在第 **94** 行（`POST /ctl/create`）；共 **27** 条路由注册 |
| `Backend` 接口 | `shared/web/types.ts:75` → `export interface Backend {` |
| 管理台 embed | `internal/server/server.go:23` → `//go:embed admin.html` |
| Web→daemon 适配器 | `deploy/docker/web-build/web.ts:4-6` 导入 `shared/web/types.js` 与 `shared/web/ui.js` |

## 5. 断链清单

1. **`scripts/build-web.sh` 输出目录缺失**：脚本写 `deploy/docker/web/`，该目录已移出；运行前需 `mkdir -p deploy/docker/web` 或改输出路径。另：`esbuild` 不在 PATH。
2. **`shared/web` 无自带 TypeScript**：没有 `shared/web/node_modules/typescript`，`npx tsc` 会解析到错误的 npm 包；可借用 `/workspace/tj-web/site/node_modules/typescript`（v5.9.3，`--noEmit` 已验证通过）。
3. **`snetbind/` 缺失**：Android 平台文件仍在 `internal/client`，但 gomobile 绑定层不存在，Android 目标不可构建/验证。
4. **`VERSION` 缺失**：版本锚点不存在；`check-version.sh`、`bump-version.sh`、`build-release.sh` 也已移出，版本校验不可做。

## 6. 缺失清单

当前工作区明确缺失：

```text
VERSION
snetbind/
.github/
dist/
Android SDK/NDK（ANDROID_HOME 为空）
java / javac（JAVA_HOME 为空）
```

Go 工具链已装：`go1.26.4`（`/usr/local/go`，`/usr/local/bin/go`）。
gcc 已装：`gcc 12.3.1`（openEuler 12.3.1-110）。

相关缺失（收缩时移出）：

```text
deploy/docker/web/
scripts/project-audit.sh
scripts/check-version.sh
scripts/bump-version.sh
scripts/build-release.sh
scripts/rollout-*.sh
docs/RELEASE.md
docs/DEPLOYMENTS.md
docs/ANDROID.md
docs/archive/
docs/releases/
.archify/
.opencode/skills/
PROJECT-STRUCTURE.md
AGENTS.md
```

## 7. 与 atlas 差异表

| Atlas 声称的路径/包/命令 | 实际 | 状态 |
|---|---|---|
| `cmd/server/main.go` | 213 行 | 存在 |
| `cmd/relay/main.go` | 98 行 | 存在 |
| `cmd/client/snetd/` | 3 文件 / 207 行 | 存在 |
| `cmd/client/snetctl/` | 1 文件 / 408 行 | 存在 |
| `internal/buildinfo` | 2 文件 / 50 行 | 存在 |
| `internal/client` | 30 文件 / 11,406 行 | 存在 |
| `internal/constants` | 1 文件 / 7 行 | 存在 |
| `internal/protocol` | 4 文件 / 741 行 | 存在 |
| `internal/relay` | 4 文件 / 1,132 行 | 存在 |
| `internal/server` | 19 文件 / 13,459 行 | 存在 |
| `internal/server/admin.html` + `go:embed` | 4,599 行；`server.go:23` | 存在 |
| `shared/web/` | 8 文件 / 3,008 行 | 存在 |
| `deploy/docker/web-build/web.ts` | 280 行 | 存在 |
| `deploy/docker/web/` | 目录不存在 | 缺失 |
| `scripts/build-web.sh` | 脚本存在；输出目录与 `esbuild` 缺失 | 存在但断链 |
| `scripts/project-audit.sh` | 不存在 | 缺失 |
| `scripts/check-version.sh` / `bump-version.sh` / `build-release.sh` / `rollout-*.sh` | 不存在 | 缺失 |
| `VERSION` | 不存在 | 缺失 |
| `snetbind/` | 不存在 | 缺失 |
| `.github/workflows/` | 不存在 | 缺失 |
| `.archify/` | 不存在 | 缺失 |
| `.opencode/skills/snet-*` | 不存在 | 缺失 |
| `docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md` | 存在 | 存在 |
| `docs/ONLINE-STATUS-LOGIC.md` | 存在 | 存在 |
| `docs/PROJECT-ATLAS.md` | 存在 | 存在 |
| `PROJECT-STRUCTURE.md` / `AGENTS.md` | 不存在 | 缺失 |
| `dist/` | 不存在 | 缺失 |
| `go test ./...` | 10 包：5 包有测试全通过，0 失败，0 跳过，5 包无测试文件 | 通过 |
| `go vet ./...` | exit 0，无输出 | 通过 |
| `go build ./...` | exit 0，无输出 | 通过 |
| `go test -race ./...` | exit 0；5 个有测试包全通过，0 失败，0 跳过 | 通过 |
| `GOOS=darwin/linux GOARCH=arm64 go build ./...` | Go 已装但未跑 | 待验证 |
| `cd shared/web && npm i -D typescript && npx tsc --noEmit` | 项目内无 TypeScript；外部 tsc 5.9.3 通过 | 项目内无法验证 / 外部已验证 |
| Android 交叉编译 | 无 `snetbind/`、无 SDK/NDK/JDK | 无法验证 |
| 版本一致性校验 | 无 `VERSION`、无校验脚本 | 无法验证 |
| `internal/client` Android 平台文件 | `device_android.go` 等存在 | 存在 |

## 8. 可研发边界

| 能力 | 当前状态 |
|---|---|
| 阅读/编辑 Go 源码（server/relay/snetd） | 可做（纯文本） |
| 编译/测试/静态检查 Go | 可做：`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 均通过 |
| 阅读/编辑 `shared/web` TypeScript | 可做 |
| Web 类型检查 | 可借用外部 tsc v5.9.3；项目内 TypeScript 缺失 |
| Web 资产构建（`build-web.sh`） | **不可做**：缺 `deploy/docker/web/` 输出目录与 `esbuild` |
| Android 构建/验证 | **不可做**：缺 `snetbind/`、Android SDK/NDK、JDK |
| 发布/版本校验/产物 | **不可做**：缺 `VERSION`、发布脚本、CI、`dist/` |
| Git 操作 | 可做（本地仓库，无 remote） |

Go 环境（2026-11-07 固化）：

- Go version: `go1.26.4 linux/amd64`
- GOROOT: `/usr/local/go`
- GOPROXY: `https://goproxy.cn,direct`
- GOTOOLCHAIN: `local`

Go 测试基线（2026-11-07）：`go vet ./...` exit 0；`go test ./...` 与 `go test -race ./...` 均为 10 个包，5 个有测试的包全部通过，0 失败，0 跳过，5 个包无测试文件。

NAT 映射 unclaim 验证（2026-11-07）：

- `TestUnclaimTearsDownUPnPMapping`：`unclaim` 的异步 teardown 实际调用 UPnP `DeletePortMapping`，deleted port 正确。
- `TestUnclaimSkipsTeardownForNatPMPAndPCP`：NAT-PMP/PCP 依赖 lease，unclaim 不发 Delete。
- `TestReclaimWaitsForPendingTeardown`：`pending[port]` 保护；`waitPending` 在旧 teardown 完成前阻塞，re-claim 不会先建立新映射。

POST /ctl/nat 验证（2026-11-07）：

- `TestCtlSetNATMappingTearsDownOnDisable`：HTTP POST `/ctl/nat` enabled=false → 204；配置持久化 false；同步 teardown 实际调用 UPnP `DeletePortMapping`。
- `TestCtlSetNATMappingEnablePersistsWithoutTeardown`：enabled=true → 204；配置持久化 true；不发 Delete。

NAT 映射协议优先级验证（2026-11-07）：

- `TestMapNATPortWith`：UPnP 成功 → `via=upnp`，NAT-PMP/PCP 各 0 次；UPnP 失败 → NAT-PMP；NAT-PMP 失败 → PCP；无 gateway → 返回错误且不 fallback。
- `mapNATPortWith` 使用 `defaultGW func() string`，仅 UPnP 失败且 gw 为空时调用 `defaultGatewayIP()`，保持原调用时机。

NAT 映射覆盖收口（2026-11-07）：

已闭环（P0/P1）：
- #2 WAN IP 门禁：`TestMappedEndpointFor`（matching WAN → `externalIP:externalPort`；mismatch/empty observed/no mapping → ""）。
- #3 unclaim teardown：`TestUnclaimTearsDownUPnPMapping`、`TestUnclaimSkipsTeardownForNatPMPAndPCP`、`TestReclaimWaitsForPendingTeardown`（UPnP Delete 实际发生；NAT-PMP/PCP 不 Delete；`pending` 等待旧 teardown）。
- #5 POST /ctl/nat：`TestCtlSetNATMappingTearsDownOnDisable`、`TestCtlSetNATMappingEnablePersistsWithoutTeardown`（204；持久化；disable 同步 teardown；enable 不 teardown）。
- #7 协议 fallback：`TestMapNATPortWith`（UPnP > NAT-PMP > PCP；无 gateway 不 fallback）。

待补（P2/P3）：
- #1 租约/续期：未断言 `natMapLeaseSec=5m` 进入请求，未测试 `natRenewInterval=2m` 的 loop/续期；阻塞：缺 ticker/clock 注入点。
- #8 平台网关解析：`gateway_darwin.go` / `gateway_proc.go` / `gateway_other.go` 的 `defaultGatewayIP` 无测试；阻塞：硬编码 `/proc/net/route`、`netstat`，需文件/命令注入点。
- #4 `/ctl/status.natMapping`：仅测 `natManager.status()`，未测 HTTP `/ctl/status` JSON 接线；阻塞：需完整 ctl 路由测试（可复用 `ServeCtl`）。
- #6 失败日志限流：只测窗口内重复抑制，未测超过 `natErrorLogInterval=10m` 后重新记录；阻塞：缺 clock 注入点。
- #9 teardownMapping 平台分支：`ig2` 已验证，`ig1`/`ppp` 分支未测；阻塞：需可注入 client factory 或按 kind 的 fake UPnP service。

KickNode 端口释放修复（2026-11-07）：

- `TestKickNodeReleasesNodePort`：owner kick 后 `nodePorts`、`portToNode`、`netByPort`、`nodeRelayHost` 均清理，`relayRelease(port)` 被调用。
- 修复 commit `20058cd`；`KickNode` 现在与 `RemoveNode`/`AdminRemoveNode` 一致，在删除节点前调用 `releaseNodePortLocked`。

节点移除 relay 索引清理（2026-11-07）：

- `TestKickNodeClearsRelayIndexes`、`TestRemoveNodeClearsRelayIndexes`、`TestAdminRemoveNodeClearsRelayIndexes`：节点移除后清 `relayFlowByNode`、`flowByNode`、`ctrlHost`、`nodeByHost`、`relayFlowBadUntil`；`nodeByHost` 删到空时 host + `hostSeen` 一起删。
- 修复 commit `1aeeb51`；`clearNodeRelayIndexesLocked` 在 `releaseNodePortLocked` 后、`delete(ns.nodes)` 前调用。

AdminDeleteDevice / reviveNode 清理（2026-11-07）：

- `TestAdminDeleteDeviceReleasesPortAndClearsIndexes`：设备删除释放 per-node port，清 `relayFlowByNode`、`flowByNode`、`ctrlHost`、`nodeByHost`、`relayFlowBadUntil`。
- `TestReviveNodeClearsRelayIndexes`：同 device 重复节点被 revive 删除时清全部 relay/host 索引。
- 两处复用 `clearNodeRelayIndexesLocked`；`AdminDeleteDevice` 同时补 `releaseNodePortLocked`（修复同类端口泄漏）。commit `cc4f0c2`。

Pending 跨重启持久化（2026-11-07）：

- `TestPendingPersistsAcrossRestart`：`NewStoreAt` 创建 pending → `Close` → 同路径重开；`PendingStatus` 仍为 pending，`OwnerApprove` 成功并返回完整凭据/IP。commit `563049c`。

Device 跨重启持久化（2026-11-07）：

- `TestDeviceRecordPersistsAcrossRestart`：`NewStoreAt` + `BindDevice` + `CreateNetwork` → `Close` → 重开；`AdminDevices` 保留 ID/name/publicKey，`DeviceNetworks` 保留网络，`ValidateDeviceToken` 原 device token 仍有效。commit `04f675d`。

删除路径覆盖收口（2026-11-07）：

- `TestOwnerDeleteNetworkCleansUp`、`TestAdminDeleteNetworkCleansUp`：整网删除释放 per-node/broadcast 端口、清 `portToNode`/`netByPort`，并删除 bbolt `networks`/`nodes`/`tokens`/`pending` 行。
- `TestAdminRemoveNodeCleansUp`：admin 移除节点执行 release + clear，token/node/relaytopo 行删除。
- `TestSweepZombiesReleasesNetworkPorts`：僵尸网络收割释放端口、清 port maps、`s.networks` 移除。
- 已知少量内存残留：`s.nodeRelayHost[netID]` 空 map 在网络删除后保留，不影响功能。

结论：当前可研发范围是 **Go 源码阅读/编辑/编译/测试（含 `-race`） + `shared/web` 编辑与外部 tsc 类型检查**；Web 打包、Android 与发布链路仍待补缺失目录/工具后再验证。

PathManager direct 来源校验与候选优先级（2026-11-07）：

- 来源校验正向：`TestPathManagerAllowsPublicPortRebinding`：public 候选 `203.0.113.5:51820`，来源 `203.0.113.5:40000` 同 IP 不同端口被接受，`dp.kind=public`、`dp.addr` 更新为新端口。
- 来源校验负向：`TestPathManagerRejectsWrongLANPort`（LAN `192.168.1.10:51820` 拒绝 `:51821`）、`TestPathManagerRejectsUnknownPublicSource`（public 候选 `203.0.113.5` 拒绝 `198.51.100.7`）、`TestPathManagerRejectsDirectFrameWhenNoCandidate`（无候选直接拒绝）。拒绝后 `dp.ready`/`dp.addr` 均不变。
- 匹配规则确认：LAN 候选要求 IP + 端口精确匹配；public/IPv6 候选只固定 IP，允许 NAT rebinding 改端口；`hintIP` 命中按 public 处理；无匹配返回空串，`noteDirectPeer` 返回 false。
- 候选优先级：`TestPathManagerCandidatePriority` 断言 `candidatesLocked` 顺序为 `[lan, ipv6, public]`；`TestPathManagerLowPriorityAckDoesNotOverrideHighPriority` 断言 LAN 已 ready 时 public ACK 被接受但不应用，`kind`/`addr` 保持 LAN。
- 测试命令：`go test ./internal/client -run 'TestPathManagerRejects|TestPathManagerAllows|TestPathManagerCandidatePriority|TestPathManagerLowPriority' -v` 全绿；`go test -race ./internal/client -run '...' -count=20` 全绿；`go test ./internal/client` 全绿。commit `746431c`。
- 验证层级：同包直接调用 `noteDirectPeer`/`candidatesLocked`；`handleDirectFrame` 调用链由既有 `TestPathManagerRelayLoopbackShims` 覆盖。
- 未发现来源校验或候选优先级缺口；10s 低优先级→高优先级主动升级探测仍为独立缺口，不在本次范围。

PathManager DATA 低优先级来源与 relay 回退（2026-11-07）：

- DATA 低优先级来源行为：`TestPathManagerAcceptsDataFromLowerPrioritySourceWhenHigherReady` 确认 LAN 已 ready 时，public 候选来源的 DATA 帧 `noteDirectPeer` 返回 true，payload 仍由 `shim.inject` 注入；`dp.kind`/`dp.addr` 保持 LAN。设计意图：不因路径抖动丢包；若评审要求严格单向，需改为 drop 并报告。
- 对称 NAT/防火墙保持 relay：`TestPathManagerSymmetricNATStaysRelay` 用真实 UDP 候选但不回 ACK，连续 `maintainDirect(now)` 覆盖 >15s；`dp.ready` 始终 false、`dp.addr` 保持 nil，`sendToPeer` 只从 relay link 发出 DATA，`directSendFrames` 不增长；`Status().peerDirect["node-b"].path` 为 `discovering`（非 `direct`）。
- 发送失败回退 relay：`TestPathManagerDirectSendFailureFallsBack` 让 `pm.directConn` 指向已关闭的 UDP conn；`sendToPeer` 的 `writeFrameTo` 失败后 `sendDrops` 增加，`markDirectFailed` 将 `dp.ready=false` 并清空 `lastProbe`，随后同一调用立即从 relay link 发出 DATA。注意 `markDirectFailed` 不清 `dp.addr`。
- 测试命令：`go test ./internal/client -run 'TestPathManagerSymmetricNATStaysRelay' -v`、`go test ./internal/client -run 'TestPathManagerDirectSendFailureFallsBack' -v` 全绿；两条 `-race -count=20` 全绿；`go test ./internal/client` 全绿。commit `cd4ebb0`。
- 未发现对称 NAT 保持 relay 或发送失败回退缺口。

PathManager 升级探测与 Status 语义（2026-11-07）：

- `markDirectFailed` 不清 `dp.addr` 的实际后果：direct 写失败后 `dp.ready=false`，但同一来源的 ACK 到达时，`noteDirectPeer` 的 `dp.addr != nil && sameUDPAddr` 分支会快速把 `dp.ready` 置回 true；这是快速恢复路径（入站 ACK 证明该来源可达），也可能在出站持续失败时反复重试。即使清空 `dp.addr`，`noteDirectPeer` 仍会因 `markReady=true` 重新学习该来源并置 ready，因此清 addr 不能阻止恢复；atlas 只要求“发送失败回退 relay”，未要求清 addr。
- Status path 语义：`dp.ready && dp.addr != nil` → `direct`（并输出 `kind`）；`dp.addr != nil && !ready` → `probing`；`dp.addr == nil` → `discovering`。atlas §3.2 只枚举 `direct|probing|discovering`，未定义边界；当前代码语义与 `TestPathManagerSymmetricNATStaysRelay` 的 `discovering` 断言一致，无发现不符。
- 10s 升级探测：`maintainDirect` 在 `dp.ready && dp.addr != nil` 时，用共享的 `dp.lastProbe` 判定，`lastProbe.IsZero()` 或距上次 >= `directUpgradeInterval`（10s）才向第一个 `pathRank` 更高的候选发 `DIRECT_PROBE`，并更新 `dp.lastProbe`；没有独立 `lastUpgrade` 字段。
- 验证：`TestPathManagerUpgradesToHigherPriorityCandidate` 在 public ready、`lastProbe=now-11s` 时收到 LAN `DIRECT_PROBE`，模拟 LAN ACK 后 `dp.kind=lan`、`dp.addr` 切到 LAN；`TestPathManagerDoesNotUpgradeBeforeInterval` 在 `lastProbe=now-5s` 时 `directSendFrames` 不增长且 LAN 无帧。两条测试 `-race -count=20` 全绿；`go test ./internal/client` 全绿。commit `8124e15`。
- 未发现 10s 升级探测缺口。

PathManager relay 重连边界（2026-11-07）：

- 10s HELLO_ACK 超时：用 `relayLink.attempt` 判定；条件为 `!ready && !attempt.IsZero() && now.Sub(attempt) > 10s`。触发后关闭旧 conn、`conn=nil`、`ready=false`、`nextDial=now+5s`，本次不立即重拨；下一次 `maintainRelays` 到达 `nextDial` 后才调用 `dialRelayLink`。
- 30s 静默：用 `relayLink.lastRecv`（atomic，秒）判定；条件为 `lastRecv > 0 && now.Unix()-lastRecv > 30`。触发后同样关旧 conn、`conn=nil`、`ready=false`、`nextDial=now+5s`。
- 5s 重拨配合：`conn == nil` 时，仅当 `nextDial.IsZero() || now >= nextDial` 才把 `nextDial` 推到 `now+5s` 并加入 dial 队列；`dialRelayLink` 设置新 conn、`attempt=time.Now()`、`lastRecv=0` 并立即发 HELLO。
- 多 link：`maintainRelays` 逐 link 独立处理；超时/静默只清该 link 的 conn/ready/nextDial，healthy link 的 conn/ready/attempt/lastRecv 不变。`pickRelayLocked` 仍返回第一个 ready 且 `lastRecv` 在 30s 内的 link；`sendToPeer` 继续走该 link。
- 验证：`TestPathManagerHelloTimeoutReconnects`、`TestPathManagerDoesNotReconnectBeforeHelloTimeout`、`TestPathManagerSilentLinkReconnectAfter30s`、`TestPathManagerKeepsLinkBefore30sSilence`、`TestPathManagerReconnectsOnlySilentRelayLink` 全绿；`-race -count=20` 全绿；`go test ./internal/client` 全绿。commit `ed9c0c7`。
- 未发现 relay 重连边界缺口。

PathManager 覆盖收口（2026-11-07）：

| # | 缺口 | 状态 | 对应测试 |
|---|---|---|---|
| #1 | 候选优先级 `lan > ipv6 > public` | 已闭环 | `TestPathManagerCandidatePriority`、`TestPathManagerLowPriorityAckDoesNotOverrideHighPriority` |
| #2 | 已建立低优先级路径时每 10s 升级探测 | 已闭环 | `TestPathManagerUpgradesToHigherPriorityCandidate`、`TestPathManagerDoesNotUpgradeBeforeInterval` |
| #3 | direct 帧来源校验正/负向 | 已闭环 | `TestPathManagerRejectsWrongLANPort`、`TestPathManagerRejectsUnknownPublicSource`、`TestPathManagerAllowsPublicPortRebinding`、`TestPathManagerRejectsDirectFrameWhenNoCandidate` |
| #4 | 5s DIRECT_PING 节奏 | 已闭环 | `TestPathManagerDirectPingCadence`、`TestPathManagerDoesNotPingBeforeInterval` |
| #5 | direct 发送失败回退 relay | 已闭环 | `TestPathManagerDirectSendFailureFallsBack` |
| #6 | 对称 NAT/防火墙保持 relay | 已闭环 | `TestPathManagerSymmetricNATStaysRelay` |
| #7 | 10s HELLO_ACK 超时重拨 | 已闭环 | `TestPathManagerHelloTimeoutReconnects`、`TestPathManagerDoesNotReconnectBeforeHelloTimeout` |
| #8 | 30s 静默重拨 | 已闭环 | `TestPathManagerSilentLinkReconnectAfter30s`、`TestPathManagerKeepsLinkBefore30sSilence` |
| #9 | 多 link 只重连失败实例 | 已闭环 | `TestPathManagerReconnectsOnlySilentRelayLink` |
| #10 | `peerDirect` 全字段 | 已闭环 | `TestPathManagerStatusPeerDirectFields`（direct/probing/discovering 子测试） |
| #11 | 真实 NAT rebinding/IPv6 防火墙/Docker 临时端口/多 relay 跨实例 | **需真机，待验证** | 建议 `TestRelayV2FourNodeHardware`（未实现）；atlas §7“Relay v2 直连路径” |

补充说明：

- #4 ping 分支：`dp.ready && dp.addr != nil` 时，`dp.lastPing.IsZero()` 或 `now.Sub(dp.lastPing) >= directPingInterval`（5s）才发送 `DIRECT_PING`；`lastPing` 在锁内先更新为 `now`，再实际写 socket。15s 回退由 `sendToPeer` 的 `lastRecv <= directDeadAfter` 判定；`maintainDirect` 在 `>15s` 无接收时清 `ready=false`/`addr=nil`。
- #10 字段：`path` 为 `direct|probing|discovering`；`kind` 仅在 direct 时输出；`addr` 在 `dp.addr != nil` 时输出；候选地址实际是分开的 `lan`/`ipv6`/`public` 字符串字段，**没有聚合的 `candidates` 数组**；`lastRecvSec` 是相对秒数（`int(now.Sub(lastRecv).Seconds())`），不是 Unix 秒；`peerDirect` 条目仅在 `peerKeys[key]` 非空时出现。
- `markDirectFailed` 不清 `dp.addr`：写失败后同一来源 ACK 会经 `sameUDPAddr` 分支快速恢复 `ready`；这是快速恢复，但出站持续失败时会反复重试。清 `addr` 也不能阻止，因为 ACK 会重新学习来源。
- Status 语义边界：`ready && addr != nil` → `direct`；`addr != nil && !ready` → `probing`；`addr == nil` → `discovering`。
- #11 验证命令（真机，尚未执行）：
  - 四节点（同 LAN / 公网 / IPv6 / Docker）互 ping：`ping -c 20 <peer>`
  - 吞吐：`iperf3 -c <peer> -t 30`
  - 跨 relay：配置两个 `SNET_RELAY_V2_ENDPOINTS`，检查 `curl -s http://127.0.0.1:19432/ctl/status | jq '.relayPath.peerDirect, .relayPath.relayIndex'`
  - NAT rebinding / IPv6 防火墙 / Docker 临时端口：改变映射后确认 `peerDirect.addr` 更新或回退 relay；不得把本轮单元测试当作真机已验证。

Server 鉴权覆盖（2026-11-07）：

- token 隔离负向：`TestDeviceTokenNotAcceptedAsNodeBearer` 设备 token 放 `Authorization` 请求 `/peers` → 401；`TestNodeTokenNotAcceptedAsDeviceToken` node token 放 `X-Device-Token` 请求设备网络列表和 bound `auth-status` → 401；`TestDeviceTokenNotAcceptedForOwnerRoute` 设备 token 请求 owner `PATCH /api/v1/networks/{nid}` → 404（`ownerNID` 有意隐藏网络存在性，非 401/403，但仍拒绝）。未发现互相冒充被接受。
- sanitizeLog 当前行为：只把控制字符转义为 `\xNN`，**不 redact** token/privateKey/pairing code；仅访问日志的 path/client IP 调用它，其他 `log.Printf` 不经过它。逐条扫描 `internal/server/*.go` 的日志调用未发现当前直接记录 token/privateKey/pairing code/Authorization/X-Device-Token/密码/auth code；`internal error: %v` 的 err 来自固定 sentinel、校验错误或 bbolt 错误，未发现携带请求体/secret。结论：当前未发现泄漏，但不是 redaction 保证。
- 日志测试：`TestSanitizeLogEscapesControlChars` 锁定 `\n`/`\x7f`/`\x00` → `\xNN`；`TestServerLogsDoNotContainSecrets` 捕获访问日志 + 无效 subnet 触发的 internal error，断言不含 token、`privateKey`、`pairingCode` 标记。
- 公开 enrollment 限流/门禁：`TestAnonymousEnrollmentRateLimited` 选 `/devices/bind`（最小 20/min/IP）作代表，20 次未限流、第 21 次 429；`TestRequireDeviceAuthBlocksAnonymousCreateJoinRegister` 在 `RequireDeviceAuth=true` 时无绑定 deviceId 的 create/join/register 均 403，绑定 device 的 create/join/register 分别 201/200/200。注意门禁校验的是 DeviceID 的授权码绑定，不是 `X-Device-Token` 头。
- `admin.html` 公开是设计：`TestAdminWebRoot` 验证无凭据 `/admin` 仍 200（首次 bootstrap/login UI），不列为缺口；管理 API 仍由 `requireAdmin` 保护。
- 仍待补（低优先）：#8 admin session 过期、#9 pending poll 限流/一次性消费、#11 static-token logout no-op。
- 测试命令：token 隔离 `-race -count=20` 全绿；enrollment `-race -count=10` 全绿；sanitize/log 全绿。commits `a35eee4`、`4b9dbed`、`37f5480`。

Server 路由鉴权矩阵（2026-11-07）：

- #4/#5/#10 闭环：`TestNodeTokenRoutesAuthMatrix` 覆盖 7 条 node-token 路由；`TestAdminRoutesAuthMatrix` 覆盖 4 条 admin 路由；`TestAdminDisabledReturns404` 覆盖 admin 未配置语义。
- Node-token 实际矩阵（无 token / garbage / 跨网络 token / 本网络合法 token；pending 另有 member）：
  - `PUT .../direct-endpoints`：401 / 401 / **204** / 204
  - `GET .../relay-ticket`：401 / 401 / 401 / 200
  - `PATCH .../subnets`：401 / 401 / **204** / 204
  - `GET .../exists`：401 / 404 / 404 / 200
  - `DELETE .../nodes/{nodeID}`：401 / 401 / **204** / 204
  - `POST .../pending/{id}/approve`：401 / 404 / 404 / member 401 / 200
  - `POST .../pending/{id}/deny`：401 / 404 / 404 / member 401 / 204
- **当前行为 vs atlas 期望，待决策**：`direct-endpoints`、`subnets`、`delete-node` 的 handler 忽略路径 `nid/nodeID`，直接按 bearer token 操作 token 自身网络/节点；因此跨网络合法 token 返回 204，而不是 atlas/矩阵预期的 401/404。测试当前锁定的是“路径被忽略、操作自身资源”的现状，不视为符合 §5/§4 不变量 7；需后续决定是否在 handler 中校验路径网络/节点。
- owner 语义：`ownerNID` 对未知/跨网络 token 统一返回 404（隐藏网络存在性）；本网络 member token 通过 `ownerNID` 后被 `requireOwnerLocked` 拒绝，pending approve/deny 返回 401。
- Admin 实际矩阵（无 token / garbage / static token / session token）：4 条路由均为 401 / 401 / 2xx / 2xx；`GET /admin/networks/{nid}` 200，`PATCH authcodes/{id}/renew` 204，`DELETE devices/{id}` 204，`PATCH devices/{id}` 204。admin 未配置时 4 条均 404。
- 测试命令：`go test ./internal/server -run 'TestNodeTokenRoutesAuthMatrix|TestAdminRoutesAuthMatrix|TestAdminDisabledReturns404' -v` 全绿；`-race -count=10` 全绿；`go test ./internal/server` 与 `-race` 全包全绿。commits `abbfe59`、`5a1903c`。
- 仍待补：见上条 #8/#9/#11。
