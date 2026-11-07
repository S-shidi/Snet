# SNET 项目全局图谱（Agent Atlas）

> 这是跨会话的项目记忆索引，不是完整设计文档。
> 读取顺序：本文件 → 匹配的 `.opencode/skills/snet-*` → 代码/测试。
> 维护规则见 §8；结构漂移用 `./scripts/project-audit.sh` 检查。

## 0. 新鲜度与真相源

```text
last_verified:        2026-11-07
verified_code_commit: c379790（快照导入基线，与上游 c379790 未比对）
verified_worktree:     clean（v1.3.0 release commit `c379790`；Go/Android/版本检查已通过，VPS + NAS 已部署）
state:                2026-10-27 v1.3.0 已发布；VPS `server` 与 NAS `snetd` 均已升级到 1.3.0（commit `8359cb7`），Relay v2 未启用。VPS 备份 `/usr/local/snet/backups/1.3.0-20261027_015222`；NAS 使用版本化 runtime `snet-runtime-1.3.0-r2.tar.gz`（sha256 `dd63bba8…366011`，build `20261027-2`），旧 compose 备份 `/volume1/docker/snet/compose.yaml.bak-1.3.0-20261027_020932`，旧 tar `/srv/snet-runtime/snet-runtime-1.2.0-backup.tar.gz`；NAS 节点在线。本版包含 Relay v2 控制面直连端点/NAT 类型、同 LAN > IPv6 > 公网映射优先级、多 relay 全实例 session/选路、控制帧按 IP 限流。验证：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`、`check-version 1.3.0`、`build-release`/checksums、Android 单测与 debug APK 30.7M（versionCode 10102）、VPS `/healthz` `serverVersion=1.3.0`。Relay v2 启用仍是 flag day，必须全部客户端先升级；当前生产仅 NAS 在线，phone/SDserver 约 33h 未上报，本次部署保持 Relay v2 关闭。
verified_with:        go test ./...; go vet ./...; go build ./...;
                      go test -race ./...;
                      ./scripts/check-version.sh; sh -n scripts/build-release.sh;
                      sh -n scripts/rollout-vps.sh; ./scripts/project-audit.sh
                      （2026-10-27 本地验证通过；Windows NSIS、Android signed release、
                      Docker/cargo、真机 Relay v2 未验证）

`verified_code_commit` 是最后一次跑完整验证的**代码**提交，不要求等于 HEAD；
只改文档/atlas 的提交不会让它失效。若代码路径有变化，必须重新验证并更新该字段。

真相源优先级：

1. 代码与测试（`internal/`、`cmd/`、`shared/`）
2. `DESIGN.md`（系统设计、数据模型、端口、API）
3. `README.md`（使用、构建、部署入口）
4. 本 atlas（全局索引与不变量）
5. 领域 skill（操作规范；若与代码冲突，以代码为准并修正 skill）
6. `PROJECT-STRUCTURE.md`（2026-09-13 的目录快照，可能过期，不作为事实源）

遇到冲突：先验证代码，再修文档；不要根据旧文档猜实现。

## 1. 一句话与边界

SNET 是面向个人/家庭/小团队的 WireGuard 虚拟组网工具。Go 服务端只做协调、探测和中继；
真正的数据面在 `snetd` 的 WireGuard 隧道里。所有 UI（Tauri、Android、Docker Web）
只访问本机 `snetd` 的 `/ctl/*`，不直连协调服务器。

核心边界：

- 服务端不转发业务数据，除非客户端选择 relay fallback。
- 客户端协调服务器地址是写死的，`snetctl` 不接受任意 `--server`。
- Android 是原生 Compose + gomobile；Docker/桌面共享 TypeScript Web UI。
- Rust 桌面端只做 HTTP 代理，不实现网络/隧道逻辑。
- VPS 只运行协调服务端；成员客户端在 NAS、SDserver、手机、桌面等主机运行。

## 2. 组件地图

| 组件 | 入口 | 职责 | 权威文件 | 领域 skill |
|---|---|---|---|---|
| 协调服务器 | `cmd/server/main.go` | HTTP API、bbolt、旧 relay、probe、ticket 签发、管理台 | `internal/server/` | `snet-go-core` |
| Relay v2 | `cmd/relay/main.go` | 无状态 UDP session relay、按节点 ID 精确转发、metrics | `internal/relay/` | `snet-go-core`、`snet-deploy-ops` |
| Go 守护进程 | `cmd/client/snetd/` | 隧道、peer 轮询、NAT 探测、密钥轮换、`/ctl/*`、Relay v2 shim | `internal/client/` | `snet-go-core` |
| 协议层 | `internal/protocol/` | 共享类型、版本、`snet://` 链接、Relay v2 帧 | `types.go`、`link.go`、`relay.go` | `snet-go-core` |
| 共享 Web UI | `shared/web/` | Docker 控制台与 `Backend` 契约 | `ui.ts`、`types.ts`、`subnet.ts` | `snet-web-ui` |
| Docker 客户端 | `deploy/docker/` | snetd + nginx + 构建后的 Web 资产 | `deploy/docker/web/`, `deploy/docker/web-build/` | `snet-web-ui`、`snet-deploy-ops` |
| 部署/运维 | `deploy/`、`scripts/` | VPS/NAS/macOS rollout、排障 | `scripts/rollout-*.sh`、`deploy/*.md` | `snet-deploy-ops` |
| 版本/发布 | `VERSION`、`scripts/` | 版本同步、发布产物、CI/Release、回滚 | `internal/buildinfo/`、`docs/RELEASE.md` | `snet-deploy-ops` |
| 架构图 | `.archify/` | 产品、用户全链路、生命周期、NAT 时序、bbolt ERD | 五个最终 HTML | 本 atlas |

## 3. 三条主流程

### 3.1 入网与审批

```text
创建网络 → 分配 10.88.0.x/24 → pairingCode/token
  → 分享 snet://join?... → 客户端 join
  → ApprovalRequired=false 直接入网；true 进入 pending
  → owner/admin approve → 分配 IP/token → peers 列表
```

- 固定协调地址：`https://snet.uizhi.eu.org:8090`。
- 设备身份：Android 硬件派生；其他平台持久化 `device.id`，与 WireGuard 密钥共存。
- 设备授权（可选）：管理台生成 code → 客户端 `/ctl/bind` → `BindDevice` 绑定 deviceID/公钥并返回 deviceToken → `SyncNetworks` 拉取该设备已建/已入网络并恢复隧道。
- 启动复查：有 `deviceToken` 时 `snetd` 启动后立即查 `auth-status`；有效则恢复/同步网络，过期则跳过本地恢复并保持离线；`Daemon.Start` 幂等，Android VPN 重启复用后台循环。
- 启动网络同步：服务端 `DeviceNetworkDetails` 每次调用都会轮换节点 token，`SyncNetworks` 必须把新 token 写回已存在网络并清掉旧 `serverState`；`pingServerState` 的 404 若发生在 token 已变化之后要重试，不能直接标记“已删除”。
- 服务端 `Store` 是 bbolt + 内存缓存；新增持久字段必须走 `persist*`。
- 成员视角的 peer 列表保留 `AllowedSubnets`（数据面安装 AllowedIPs/路由需要），
  只清空 `Role`；不要恢复 `n2.AllowedSubnets = nil`，否则 managed ownerless 网络的
  子网共享永远不可达。

### 3.2 数据面：探测、直连、中继、NAT 映射

```text
每 15s UDP probe :8091 → 观测公网 IP:port → SetEndpoint
  → peer 直连；20s 未握手 → relay
  → direct: observed port ±1..±8 共 17 个候选
  → 失败候选隔离到 peer endpoint 变化；不再每 300s 盲重试
  → direct 连续 3×10s 仅 TX 无 RX（TX≥512B）→ relay
```

- Relay v2（可选）：服务端签发 1h HMAC ticket，客户端连接 `8092/udp`，
  HELLO 建立 session，DATA 按 `recipientID` 精确转发；relay 不解 WG，MTU `1320`。
  客户端每个 peer 用 `127.0.0.1:Pi` loopback shim，WG endpoint 不随路径变化。
  直连升级：客户端从独立 v4/v6 UDP socket 向 relay 注册公网映射，经控制连接查询 peer
  映射，并通过 `PUT .../direct-endpoints` 发布同 LAN、IPv6 直连 socket 与 NAT 类型。
  候选优先级 `lan > ipv6 > public`；已建立低优先级路径时每 10s 继续探测更高候选。
  双方 `DIRECT_PROBE/ACK` 成功后直发 `RelayTypeData`；只接受来源匹配已知候选的
  direct 帧（LAN 精确端口，公网/IPv6 允许 NAT rebinding 改端口）。5s ping、15s 无包或
  发送失败回退 relay；对称 NAT/防火墙阻断时保持 relay。多 relay 时每个实例都维持
  HELLO/PING session 并接收 direct 注册，发送优先第一个 ready 实例；单实例 10s 无
  HELLO_ACK 或 30s 静默只重连该实例。接收端在所有 relay 都有 session，因此跨 relay
  选路仍可互通；relay 控制帧按源 IP 限速，DATA 不限速。旧 direct-first 路径仅在
  Relay v2 未启用时保留。`/ctl/status.relayPath.peerDirect` 按 peer 暴露
  `path=direct|probing|discovering`、`kind=lan|ipv6|public`、`addr`、候选地址与
  `lastRecvSec`；顶层暴露 `relayCandidates`/`relayReady`/`relayIndex`/`natType`。
- 旧 relay：每网络一个端口，懒绑定；池：`51820-52075`。
- relay 流归属按 `host:port` 独立 1s 节流；共享出口无法唯一归属时不猜节点，
  从所有已知 sibling 的 node port fan-out，接收端 NAT 只接受匹配映射的副本。
- 成员主机 Docker 客户端 WG 端口：`52100+`，必须避开 relay 池；VPS 不再运行客户端。
- 自动端口映射：UPnP-IGD / NAT-PMP / PCP；默认开启，
  `NATMapping *bool` 为 nil/true 时启用，false 关闭；租约 5m、每 2m 续期。
- `/ctl/status.natMapping` 暴露 `enabled`/`mappings`/`errors`；`POST /ctl/nat`
  持久化开关，关闭时立即 teardown。桌面/Web/Android 设置页均可操作。
- 只有网关 WAN IP 与 relay 观测公网 IP 一致时，才把映射端点标记
  `EndpointMapped=true` 广播给 peer；否则继续用观测端点 + relay。
- `/ctl/status` 的 `peerPaths` / `peerQuality`（旧路径）与 `relayPath.peerDirect`（Relay v2）是排障入口：先判断 direct 还是 relay。
- 容器化客户端（NAS/SDserver）的 TUN 在容器命名空间内：`10.88.1.x` 只能访问该
  容器内监听的服务（如 `:8080`）；NAS 宿主 DSM/SSH 需 host network 或显式 DNAT。
- Docker 端口映射会让对端看到 NAT 源端口而非自报 `52100`，直连候选可能单向；
  此类节点以 relay 为准，客户端用 `badDirect` 隔离失败候选。容器化客户端可用
  `SNET_LOCAL_IP=<宿主 LAN IP>` 让 `LocalEndpoint` 上报宿主地址并保留运行时端口
  （compose 端口需 1:1 映射）。
- NAS 无 SSH 时走 DSM Container Manager 项目（`/volume1/docker/snet`）：实测
  `build_stream` 只重建容器、不重建 `snetd:latest` 镜像，改 Dockerfile 不会生效。
  当前方案是在 compose 的 `entrypoint` 里从 VPS 产物服务下载
  `snet-runtime.tar.gz`，校验固定 sha256 后解包到 `/` 再 `exec /entrypoint.sh`；
  NAS 必须同时发布 daemon 实际使用的 `51820/udp` 并设置 `SNET_LOCAL_IP`。
  代价是容器重启依赖该产物服务（`snet-runtime-http.service`），升级镜像前不要下线。
- Android `RoutePlanner` 只注入已加入网络的 `subnet` + peer `allowedSubnets`，
  不再固定注入 `10.0.0.0/8`；无网络 CIDR 时回退到 active IP `/32`。
  本机 LAN 若覆盖 overlay 子网、包含 overlay IP、或与 peer 子网重叠则不排除并告警。
  `excludeRoute` 仅 API 33+ 可用（之前误用 Q 门禁）。`DetectLocalSubnets` 的
  nil 结果必须在 Go/Kotlin 两侧都归一化为空数组，不能把 `"null"` 传给 `JSONArray`。
- 同公网出口的 sibling（如 SDphone/SDnas/SDserver 共享 `39.180.206.133`）不能用
  公共 IP + 宿主端口做 hairpin 直连：握手会间歇成功但单播丢包极高。客户端对
  `Endpoint` 落在自身 `PublicIP` 的 peer 禁用该 direct 回退，固定走 relay。

### 3.3 UI 与本地控制

```text
UI (Tauri / Android / Docker Web) → snetd /ctl/* (127.0.0.1:19432)
snetd → 协调服务器 /api/v1/* (HTTPS)
```

- `/ctl/*` 由配置同目录的 `ctl-token` 鉴权；绝不输出 token。
- Docker Web 的 `/ctl/*` 由 nginx 注入 `X-Ctl-Token` 与 `X-Snet-Web: 1`；
  `newCtlAuthHandler` 对 Web 请求额外要求有效 Web session（`Authorization: Bearer`），
  Tauri/Android/snetctl 仍只用 ctl-token。无 Web 密码时保持首次设置开放。
- Web create 的 `Backend.create` 必须返回 `snet://join?nid=...&code=...&server=...`；
  缺链接会导致创建后无法复制邀请。
- 设备级开关走 `POST /ctl/nat`，不要塞进按网络维度的 `/ctl/settings`。
- `shared/web/types.ts` 的 `Backend` 是 UI/平台唯一接缝；`desktop.ts` 和 `web.ts`
  必须同时实现。
- 改 `shared/web/*.ts` 后必须跑 `./scripts/build-web.sh`，否则 Docker 产物是旧版。
- Android 的 Go↔Kotlin 契约是 JSON 字符串；`snetbind/snetcore.go` 与
  `SnetBridge.kt` 必须成对修改。

## 4. 不可违反的全局不变量

1. **固定服务器**：客户端只接受写死的协调地址；不要恢复 `--server` 任意地址。
2. **端口规划**：`8090` HTTPS、`8091` UDP probe、`51820-52075` 旧 relay、
   `8092/udp` Relay v2、`8094` Relay v2 metrics、`52100+` 成员 Docker WG、
   `19432` 本地 ctl；Relay v2 metrics 默认只监听 127.0.0.1。
3. **Android 路由**：只注入已加入网络 CIDR、active IP 与 peer `allowedSubnets`；
   不添加 `0.0.0.0/0`，不再无条件注入 `10.0.0.0/8`；排除协调服务器 IP 和
   不冲突的本机 LAN；`excludeRoute` 需 API 33+；路由集合变化时用
   `HaltTunnels()` → 重建 TUN → `onTunFdReady` 热重启。
4. **锁规则**：`Daemon.mu` / `Store.mu` 保护状态；持锁禁止网络 I/O、文件写入、
   进程 spawn；持 `d.mu` 时绝不能调 `hostname()`，用 `hostnameLocked()`。
5. **持久化**：新增需要重启保留的字段必须同时更新内存缓存与 bbolt `persist*`。
6. **API 版本**：不兼容 wire 变更要提升 `protocol.APIVersion`；主版本不匹配返回 426；
   `/healthz` 的 `apiVersion` 同步。
7. **鉴权与密钥**：`/ctl/*` 用 `ctl-token`；Docker Web 代理请求另需 Web session
   （`X-Snet-Web: 1`）；管理端用 bcrypt；日志经 `sanitizeLog`；绝不把 token、
   授权码、私钥写进仓库/日志/聊天。
8. **gomobile 契约**：Go 导出方法与 Kotlin 包装方法成对修改；只改一边运行时才崩。
9. **Web 构建**：`shared/web` 改动后必须 `scripts/build-web.sh`；不要手改
   `deploy/docker/web/` 产物。
10. **Rust 边界**：桌面 Rust 只做 `/ctl/*` HTTP 代理；不要在里面实现隧道/协议。
11. **NAT 映射**：默认开启；映射建立不等于可广播，必须校验 WAN IP == 观测公网 IP；
    离开网络/端口时及时 `unclaim`，避免路由器残留规则。
12. **平台 build tag**：`internal/client` 的平台文件必须成组补齐，否则交叉编译失败。
13. **存储 bucket**：bbolt 8 个：`networks`、`nodes`、`tokens`、`devices`、
    `pending`、`admin`、`authcodes`、`relaytopo`。
14. **CI 现实**：`ci.yml` 跑 Go `test -race`/`vet`/`build`、Web 构建和版本校验；
    Windows/Android 由独立 workflow 构建，`release.yml` 只在 `v*` tag 汇总发布。
    Windows 安装包、Android 签名 APK 和生产部署仍需人工验证，不能只看 CI 绿灯。
15. **版本**：`VERSION` 是唯一人工维护的产品版本；tag 必须是 `v$(cat VERSION)`。
    `protocol.APIVersion` 独立于产品 SemVer；发布说明必须存在于
    `docs/releases/vX.Y.Z.md`。不要手工改 `buildinfo.Version`，用
    `scripts/bump-version.sh` + `scripts/check-version.sh`。

## 5. 存储、API 与代码入口

- 服务端路由表：`internal/server/server.go`；本地 `/ctl` 路由：`internal/client/ctl.go`。
  具体端点数量以审计脚本输出为准，不在本文件硬编码。
- 服务端存储：`internal/server/store.go`；relay：`internal/server/relay.go`；
  probe：`internal/server/probe.go`。
- 客户端守护进程：`internal/client/daemon.go`；HTTP 客户端：`control.go`；
  NAT 映射：`natmgr.go`；平台网关：`gateway_*.go`。
- 协议类型：`internal/protocol/types.go`；Android 桥：`snetbind/snetcore.go`。
- 版本/发布：`VERSION`、`internal/buildinfo/buildinfo.go`、`scripts/{check-version,bump-version,build-release}.sh`、
  `.github/workflows/{ci,release,build-windows,build-android}.yml`；runbook 在 `docs/RELEASE.md`。
- 管理台：`internal/server/admin*` + `go:embed`；Docker Web 产物：`deploy/docker/web/`。
- 架构图：`.archify/architecture-snet-product-20261020-162140/`、
  `.archify/workflow-snet-user-lifecycle-20261022-143213/`、
  `.archify/architecture-snet-lifecycle-20261020/`、
  `.archify/architecture-snet-nat-path-20261020/`、
  `.archify/architecture-snet-bbolt-erd-20261020/`。

## 6. 构建、测试与验证矩阵

| 目标 | 命令 | 说明 |
|---|---|---|
| Go 全量测试 | `go test ./...` | 当前主要验证手段 |
| 竞态 | `CGO_ENABLED=1 go test -race ./...` | 需要 C 编译器；本环境曾用 apk 安装 gcc |
| 静态检查 | `go vet ./...` | 无输出即通过 |
| 全量构建 | `go build ./...` | 包含 `snetbind` |
| 版本一致性 | `./scripts/check-version.sh [vX.Y.Z]` | 校验 VERSION/buildinfo/Web/Android |
| 发布产物 | `./scripts/build-release.sh` | 生成 `dist/` 版本化二进制 + `checksums.txt` |
| 交叉编译 | `GOOS=darwin GOARCH=arm64 go build ./...`；`GOOS=linux GOARCH=arm64 go build ./...` | 验证平台文件齐全 |
| Web 资产 | `./scripts/build-web.sh` | 改 `shared/web` 后必须执行 |
| Android | `./android/build-android.sh`；或 `cd android && ./gradlew testDebugUnitTest assembleDebug --no-daemon` | 需 JDK 17/Android SDK/NDK/gomobile；tag release 需签名 Secrets；2026-10-26 `testDebugUnitTest` 通过；arm64 debug APK 构建于 2026-10-22 |
| 发布流程 | `release.yml`（`v*` tag） | GitHub Release + GHCR；部署不在 CI 内，需人工确认 |
| 结构漂移 | `./scripts/project-audit.sh` | 文件、包、路由、skill、图链接、mode 检查 |

Go 工具链注意：本环境默认 PATH 可能没有 `go`；可用
`/tmp/opencode/golang/go/bin/go`。依赖下载用
`GOPROXY=https://goproxy.cn,direct`。

## 7. 已知风险与当前 WIP

- **1.1.0 发布基础设施已提交并打 tag**：`VERSION`、`internal/buildinfo`、CI/Release、
  发布文档、Android 签名配置、桌面/Android 版本号均已本地验证；尚未部署。Windows
  NSIS 与 Android signed release 未在本机复跑，需 CI/真机确认。
- 生产 VPS/SDserver 仍为 v1.0.3；`v1.0.3` 是未提交源码部署的热修复，不补打 tag。
- **Relay v2 是 flag day**：服务端一旦下发 `RelayV2.Enabled=true`，新客户端只走 Relay v2，
  不再加入旧 relay；同网络内旧客户端与新客户端无法互通。上线顺序必须是“全部客户端升级 →
  服务端设置 `SNET_RELAY_V2_ENDPOINT`/`SNET_RELAY_V2_ENDPOINTS`”，回滚先清空服务端这些变量。当前生产未启用。
- **Relay v2 新直连路径待真机验证**：同 LAN/IPv6/多 relay（含跨实例注册）/限流已有单测与 race 覆盖，
  但尚未在 NAS/phone/SDserver 上验证 NAT rebinding、IPv6 防火墙与 Docker 临时端口。
  容器 bridge 客户端的 Relay v2 直连 socket 没有端口映射，同 LAN 直连预计只能回退 relay；
  host network 或显式 DNAT 才可能生效。`待验证`：`go test ./...`、Android
  `testDebugUnitTest`、四节点真机组合测试。
- **NAT 类型仅为观测值**：`DIRECT_REGISTER_ACK` 只能区分映射是否稳定/变化，无法可靠
  区分 full-cone 与 restricted-cone；不要用它做放行或安全决策。
- **2026-10-25 复测发现数据面回归**：`SDserver→VPS` direct 0% loss，但 `VPS→SDserver`
  relay 100% loss；VPS 把帧扇出到 NAS/phone 的共享出口映射，未到 SDserver 的实际映射。
  旧 atlas/旧评审中的“VPS↔SDserver 双向 0% loss”只代表历史配置，不能作为当前验收依据。
  详细证据、根因、方案对比与测试矩阵见
  `docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md`。修复前不要把控制面 `peerPaths`
  当作数据面真相。
- 自动 NAT 映射已随 v1.1.0 提交，涉及 Go 客户端/服务端/协议、Android 和桌面端；
  新增文件：`internal/client/natmgr.go`、`natmgr_test.go`、`gateway_*.go`。
  Go 侧已通过 `go test -race ./...`、`go vet ./...`、`go build ./...` 与 darwin/linux
  arm64 交叉编译；Android 0.14 已通过 arm64 gomobile + Gradle 单测/assembleDebug，
  Rust 桌面端仍待验证。
- `.archify/*.html` 是 Archify finalize 产物，回执记录 SHA-256；不要手工格式化、
  清行尾空格或改空白，否则图与回执哈希失配。要改图请重新走 Archify finalize。
  `.archify/workflow-snet-user-lifecycle-20261022-143213/` 已随 v1.1.0 提交；不要
  清理或重排该目录，否则 CI 的 `project-audit.sh` 会缺图。
- `opencode.jsonc` 含 NAS 主机/用户信息，已加入 `.gitignore`，不要入库。
- `.opencode/skills/` 被 `.gitignore` 忽略；本 atlas 是可追踪的持久副本，
  skill 只是检索触发器。
- `PROJECT-STRUCTURE.md` 是旧快照，含旧机器绝对路径与已删除的构建产物，不要照抄。
- 服务端测试较慢（首次约 38s）；`-race` 全量约 70s。
- NAS 当前通过启动时下载 VPS `snet-runtime.tar.gz` 完成升级（DSM `build_stream`
  不重建镜像）；`snet-runtime-http.service` 或 `/srv/snet-runtime/snet-runtime.tar.gz`
  不可用时 NAS 容器无法启动。sha256 已固定在 compose，更新产物必须同步改校验值。
- 部署脚本里的 NAS 变体可能只存在于目标机；仓库内以 `scripts/` 实际文件为准。
- 生产 `W4NSYT6E` 的 `10.7.86.0/24` 依赖 SDserver 宿主机路由
  `10.88.1.0/24 via 172.22.0.2 dev br-<id>`；已用 `snet-subnet-route.timer`
  每 30s 幂等补齐（脚本动态读取 snetd 容器 IP 与 `172.22.0.1` bridge）。
  容器/网络重建后若网关变化，timer 会重算；不要只加一次性 `ip route`。
- Web 登录密码未知时只能验证未登录 401；Playwright/Chromium 仍不可用，
  不能声称已完成浏览器登录/交互验证。
- Android/桌面/Go 三个平台可能同时存在未提交改动；提交前必须按范围审查，不要整仓盲提。

## 8. 记忆维护协议

每次任务开始时：

1. 读本文件 + 一个匹配的领域 skill。
2. 跑 `./scripts/project-audit.sh`，先看 worktree、包列表、缺失文件和 mode 变化。
3. 用 `explore` 子代理做广度搜索；主上下文只保留决策和结论。

每次任务结束时：

1. 若有结构变化（新包/路由/端口/bucket/配置字段/平台契约/构建步骤），更新本文件对应小节。
2. 若领域 skill 的事实过时，修正 skill；不要只改 atlas。
3. 在 §10 追加一行变更记录（日期、改动、验证命令）。
4. 更新 §0 的 `last_verified`、`verified_code_commit`（仅代码变化时）、`state`。
5. 再次运行 `./scripts/project-audit.sh` 和至少一个相关验证命令。
6. 不确定的事实标 `待验证` 并写出验证命令，不要写成结论。

上下文控制：

- 本文件控制在约 250 行；细节放领域 skill 或 `DESIGN.md`。
- 不把大段代码/日志粘进 atlas；只放路径、约束、命令和判断。
- 先读索引，再按需读文件；避免一次性加载整个仓库。
- 代码与文档冲突时，代码胜出，并立即修文档。

## 9. 漂移审计

```bash
./scripts/project-audit.sh
```

检查内容：Git HEAD/worktree、Go 包列表、关键文件存在性、服务端/`ctl` 路由注册数、
skill 列表、`.archify` 图链接、`git diff --check`、mode change。

它不替代测试；结构审计后仍要跑 §6 中与改动相关的验证。

## 10. 变更日志

| 日期 | 变更 | 验证 |
|---|---|---|
| 2026-10-27 | NAS 客户端升级到 v1.3.0：最终使用 `snet-runtime-1.3.0-r2.tar.gz`（sha256 `dd63bba8…366011`，含当前 nginx 配置），compose 改用版本化 URL + `SNET_BUILD=20261027-2`，DSM Project `build_stream` 重建；首发 r1 因 nginx 配置缺少 `X-Snet-Web: 1` 被丢弃并移至 `/root/snet-runtime-1.3.0-r1-rejected.tar.gz`；旧 compose 备份两个版本，旧 tar 备份 `snet-runtime-1.2.0-backup.tar.gz` | 容器 `Up (healthy)`；日志 sha256 OK、`snetd 1.3.0 (8359cb7)`；NAS 节点 `lastSeenAge=0s`；VPS `/healthz` 仍为 1.3.0，Relay v2 env 仍不存在 |
| 2026-10-27 | v1.3.0 发布并部署 VPS：tag `v1.3.0` 指向 `8359cb7`；`server` 升级到 1.3.0，保留原 env/systemd，Relay v2 未启用；备份 `/usr/local/snet/backups/1.3.0-20261027_015222` | `./scripts/check-version.sh v1.3.0`、`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`、`./scripts/build-release.sh` + checksums OK、Android 单测/APK 30.7M（10102）、VPS `/healthz` `serverVersion=1.3.0`、active、8090/8091/51820/51823 正常 |
| 2026-10-27 | 准备 v1.3.0（`c379790`）：Relay v2 补齐；新增 `direct-endpoints` 控制面、LAN/IPv6 独立直连 socket、`lan > ipv6 > public` 优先级与升级探测、NAT 回执分类、多 relay 列表/全实例 session 与发送选路、控制帧按 IP 限流与 metrics；rollout 支持 `SNET_RELAY_V2_ENDPOINTS`；版本号同步到 1.3.0/Android versionCode 10102 | `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`（含 `TestPathManagerSelectsFirstReadyRelayLink`/`TestPathManagerRegistersDirectOnEveryReadyRelay`/`TestPathManagerReconnectsSilentRelayLink`）、`./gradlew testDebugUnitTest --no-daemon`、`android/build-android.sh`（APK 30.7M）；部署/真机待验证 |
| 2026-10-27 | v1.2.0 发布并部署 VPS：`v1.2.0` tag 指向 `88dd696`；VPS `server` 升级到 1.2.0（commit `88dd696`），保留原 env/systemd，Relay v2 未启用；备份 `/usr/local/snet/backups/1.2.0-20261027_000755` | `./scripts/check-version.sh v1.2.0`、`go test ./...`、`go vet ./...`、`go build ./...`、Android 单测、`./scripts/build-release.sh`、checksums OK、VPS `/healthz` `serverVersion=1.2.0`、8090/8091/51820 正常；Relay v2/真机待全客户端升级 |
| 2026-10-26 | 准备 v1.2.0（`672f4cb`）：版本号同步到 Go/桌面/Android，新增 `docs/releases/v1.2.0.md`，CHANGELOG 记录 Relay v2 与 MTU/路由变更；Relay v2 保持默认关闭，等待全客户端升级后再启用 | `./scripts/check-version.sh`、`go test ./...`、`go vet ./...`、`go build ./...`、`cd android && ./gradlew testDebugUnitTest --no-daemon`、`git diff --check`；VPS 部署/真机验证待执行 |
| 2026-10-26 | 提交 Relay v2 组网改造（`aa31e33`）：独立 relay/ticket、服务端签发、客户端 `PathManager` 直连升级与回退、Android 路由/MTU 收紧、rollout/build 支持；工作区恢复干净。`v1.1.0` tag 保持 `6403e7d`，Relay v2 留在 Unreleased | `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`、`cd android && ./gradlew testDebugUnitTest --no-daemon`、`sh -n scripts/build-release.sh`、`sh -n scripts/rollout-vps.sh`、`git diff --check`、`./scripts/project-audit.sh`；生产部署/真机 Relay v2 待验证 |
| 2026-10-26 | Android 路由收紧：active IP 不在网络 CIDR 时补 `/32`；peer 子网包含/重叠网络 CIDR 时不再生成宽路由；本机 LAN 与网络 CIDR 任意重叠都不 exclude；补对应 `RoutePlannerTest` | `cd android && ./gradlew testDebugUnitTest --no-daemon`；真机待验证 |
| 2026-10-26 | Relay v2 直连升级：新增 `DIRECT_REGISTER/LOOKUP/PEER/PROBE/PING` 帧；relay 保存每 session 直连地址并只在同网络内返回；客户端独立 UDP socket 注册/查询/探测，5s ping、15s 无包回退 relay，WG endpoint 仍固定为 shim；`/ctl/status.relayPath.peerDirect` 暴露逐 peer 路径；补 relay 直连注册/查询、session 空闲过期与 PathManager 直连端到端测试 | `go test ./...`、`go test -race ./internal/relay ./internal/client`、`go vet ./...`、`go build ./...` |
| 2026-10-26 | Relay v2 数据面：新增协议帧/ticket/standalone relay/服务端签发路由/客户端 shim；MTU 1320；Android 去掉固定 `10/8`，改按网络 CIDR 路由并修正 `excludeRoute` API 33 门禁；rollout 可选部署 relay 并同步双 env | `go test ./...`、`go test -race ./internal/relay ./internal/client -run ...`、`go vet ./...`、`go build ./...`、`cd android && ./gradlew :app-native:testDebugUnitTest --no-daemon`、`sh scripts/build-web.sh`（未跑） |
| 2026-10-26 | VPS 改为纯服务端：卸载 `snet-web`/`snet-web-old` 容器、`snet-web` 镜像、`snetd`/`snetctl`、`/snet`；通过 admin API 删除节点 `CJEB5UBZ` 与设备 `sdvps`；`rollout-vps.sh` 改为只上传/校验 `server`；README/deploy/RELEASE/skill 同步。备份 `/root/snet-client-removal-20261026_162417` | `systemctl is-active snet-server snet-runtime-http`、`curl /healthz`、`ss -lntup`（仅 8090/8091/51820）、`docker ps` 无 snet、admin networks 剩 3 节点、`sh -n scripts/rollout-vps.sh`、`./scripts/project-audit.sh` |
| 2026-10-26 | 发布基础设施（提交 `f573d7c`，tag `v1.1.0`）：`VERSION=1.1.0` + `internal/buildinfo`，Go/桌面/Android/Docker 版本同步；新增 `ci.yml`/`release.yml`，tag 构建 GitHub Release + GHCR；Android release 签名、`build-release.sh`、VPS 回滚；补 `CHANGELOG`/`RELEASE`/`DEPLOYMENTS`/`v1.1.0` notes | `go test -race ./...`、`go vet ./...`、`go build ./...`、`./scripts/check-version.sh`、`./scripts/build-release.sh`、`sh scripts/build-web.sh`、`npx tsc --noEmit`、`android/gradlew testDebugUnitTest`、`./scripts/project-audit.sh`；Windows NSIS/Android signed release/Docker **待验证** |
| 2026-10-25 | Android 同 LAN 路由修复：`DetectLocalSubnets` 在 Go 侧 nil 归一化为 `[]`，Kotlin 侧兼容 `"null"`；`RoutePlanner` 允许 `10/8` 内更具体的本机 LAN `excludeRoute`，拒绝覆盖 `10/8`、包含 overlay IP 或与 peer 子网重叠的排除 | `go test ./...`、`go vet ./...`、`cd android && ./gradlew testDebugUnitTest --no-daemon`（新增 4 个 `RoutePlannerTest`）、`./android/build-android.sh` APK 重建成功；真机安装待 ADB 恢复 |
| 2026-10-25 | 新增四节点组网评审与测试矩阵：确认 `VPS→SDserver` relay 单向黑洞、共享出口身份歧义、Android `10/8` LAN 捕获与 `nil` 子网检测缺陷；旧三节点评审标记过期 | `docker exec snet-web ping`（VPS→SDserver 0/10、VPS→NAS 10/10、VPS→phone 9/10）、VPS tcpdump 与 relay 日志、`peerPaths`、phone `ip rule/route`；详见 `docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md` |
| 2026-10-25 | NAS 客户端升级：DSM 项目构建实测只重建容器不重建镜像；改用 compose 启动时从 VPS `snet-runtime-http.service` 下载 runtime、校验 sha256、解包并执行；NAS 发布 `51820/udp` 并设 `SNET_LOCAL_IP=10.7.86.111`；升级后 `/ctl/status` 显示 `local 10.7.86.111:51820`，phone/sdserver 不再触发同出口 hairpin direct | `[upgrade]`+sha256 校验通过、`/ctl/status` `natMapping` 存在、sdserver→NAS 10/10 0% loss、sdserver→phone 10/10 0% loss、NAS 对 phone/sdserver 保持 relay |
| 2026-10-22 | 建立全局 atlas、审计脚本、记忆维护协议；修正 deploy skill 的 bucket 数与 NAS 脚本说明 | `go test -race ./...`、`go vet ./...`、`go build ./...` |
| 2026-10-22 | 修正 DESIGN/skill 的 `/ctl`、Tauri、gomobile、Backend 计数；标记旧目录快照；补 README 索引 | `./scripts/project-audit.sh` |
| 2026-10-22 | `.archify` 架构图、6 个 snet skill、文档修正入库；atlas 状态更新 | `./scripts/project-audit.sh` |
| 2026-10-22 | 新增用户全链路流程图（安装→身份→建网/邀请/加入→审批领取→隧道→资源访问），并接入 README/DESIGN/atlas/audit | `node .agents/skills/archify/bin/archify.mjs finalize workflow ...`、`./scripts/project-audit.sh` |
| 2026-10-22 | 用户全链路图补充设备授权/重装恢复轨道：管理台生成 code → 客户端输入 → 服务端绑定并推送历史网络 → SyncNetworks 恢复 | `finalize workflow ... --out-dir .../review-7`、`visual-check ... --out-dir .../visual-check-2`、`./scripts/project-audit.sh` |
| 2026-10-22 | NAT 映射收口：运行期开关、`/ctl/status.natMapping`、`/ctl/nat`、失败日志限流、IGDv1/PPP teardown、NAT-PMP/PCP 回环测试；三端设置入口；修复 Web pending 审批参数/类型 | `go test -race ./...`、`go vet ./...`、`go build ./...`、交叉编译、`build-web.sh`、`tsc --noEmit` |
| 2026-10-22 | 启动授权复查+网络同步：`startupAuthSync` 先查 `auth-status` 再 `SyncNetworks`；过期跳过本地恢复；`Daemon.Start` 幂等、Android 入网门禁、NAT `startOnce`；修正图/文档的“托管模式需授权”“推送网络”“只存哈希” | `go test ./internal/client ./internal/server`、`finalize ... --out-dir review-9`、`visual-check ... --out-dir visual-check-3`、`./scripts/project-audit.sh` |
| 2026-10-23 | Android 0.14：全量 Compose UI（网络/详情/成员/审批/设置）、QR 扫码与邀请码、auth 过期/待审批横幅、`peerSubnets` 动态路由（`RoutePlanner` + VPN 热重建）、`UpdateSettingsJSON` 清空语义、Linux/Alpine 构建脚本、版本 0.14 | `go test ./...`、`go vet ./...`、`gomobile bind -target=android/arm64`、`./gradlew testDebugUnitTest assembleDebug --no-daemon` |
| 2026-10-24 | Android 真机问题修复：绑定/加入对话框原地 loading+错误、扫码竖屏且识别后自动加入、加入 active 网络后自动启动 VPN/请求权限、拒绝权限时断开网络、隐藏瞬态 `VPN TUN 未就绪` 错误 | `cd android && ./gradlew testDebugUnitTest assembleDebug --no-daemon`、真机重装待验证 |
| 2026-10-24 | Android 授权展示：归一化 Go 的 `authExpiresAt: null`，永久授权显示“永久”；状态页已绑定显示授权期限、未绑定显示“去绑定设备”引导；新增 `AuthExpiryTest` | `./gradlew testDebugUnitTest assembleDebug --no-daemon`、真机 uiautomator 校验“授权期限 永久” |
| 2026-10-24 | 启动网络状态误报：`SyncNetworks` 刷新服务端轮换的 node token 并清 `serverState`；`pingServerState` 对过期 token 的 404 重试，避免网络卡片误显示“已删除” | `go test ./internal/client -run 'TestPingServerStateRetriesRotatedToken\|TestSyncNetworksRefreshesExistingToken'`、`go test ./...`、真机重启保持“运行中/已连接” |
| 2026-10-24 | 客户端部署 runbook 写入 `snet-deploy-ops`：VPS systemd / 通用 Docker / NAS 步骤、设备身份与端口约束、`DeviceNetworkDetails` 轮换 token 导致 401 的排障；NAS+SDserver 用当前源码重建客户端并验证 | `go build ./...`、`build/linux-amd64` 交叉编译、NAS/SDserver `docker compose build`、`/ctl/peers` 200、`./scripts/project-audit.sh` |
| 2026-10-24 | VPS 客户端升级：用当前源码交叉编译 `snetd/snetctl`，重建 `snet-web:v1.0.1`（Alpine + host network + NET_ADMIN + `/dev/net/tun`）；授权码 `4BKJGK3Y` 经管理 API 续为永久，`/ctl/bind` 幂等重绑恢复 `W4NSYT6E`；WG 端口改 52100；重启验证 token 轮换持久化、peers 200、无 401，VPS 节点恢复在线 | `docker build`、`/ctl/peers` 200、重启后 token `dc33ec→6718e0`、NAS 侧 peers 显示 `CJEB5UBZ` online |
| 2026-10-25 | 修复 Windows NSIS 安装包回归：用 `tauri.windows.conf.json` 恢复 `bundle.resources`（snetd/wintun/license），CI 新增 Wintun 0.14.1 下载与 SHA-256 校验，deploy README 补本地构建步骤 | `node -e JSON.parse(...)`、`git diff --check`、`./scripts/project-audit.sh`；Windows 安装包构建 **待验证**（需 Windows runner 或 `npm run tauri build -- --bundles nsis`） |
| 2026-10-25 | 成员 `AllowedSubnets` 可见性修复；Docker Web `/ctl/*` 增加 Web session 门禁（nginx `X-Snet-Web` + `newCtlAuthHandler`）；Web create 返回邀请链接；VPS/SDserver 部署 v1.0.3；SDserver 生产子网 host route + `snet-subnet-route.timer` | `go test ./...`、`go vet ./...`、`go build ./...`、`sh scripts/build-web.sh`、`cd desktop && npx tsc --noEmit`；未登录 VPS/SDserver `/ctl/status` 401；VPS `/ctl/peers` 含 `allowedSubnets`；VPS→`10.7.86.1` 0% loss、HTTP 200；`10.7.86.222:18080` 200 |
| 2026-10-25 | relay 共享出口最终修复：共享 host 的 pinned flow 不再归因单节点，歧义时 fan-out 全部 sibling node port；`OnRelayFlow` 拒绝为共享 host 写 pin；新增 `SNET_LOCAL_IP` 覆盖容器自报 LAN 端点、同出口 sibling 不再走公共 IP hairpin 直连；SDserver 上报 `10.7.86.222:52100`；禁用同公网出口 sibling 的公共 IP hairpin direct；验证 phone→sdserver 持续 60 包 0% loss，四方互通 | `go test ./...`、`go vet ./...`、VPS 部署、SDserver 重建、Android APK 重建安装、phone→{sdserver,nas,vps} 0% loss、sdserver→{phone,nas,vps} 0% loss |
| 2026-10-24 | relay 共享出口修复：流归属按 `host:port` 独立节流、共享 host 不猜节点、歧义时从所有 sibling node port fan-out；客户端 direct 单通检测（3×10s TX≥512B/RX=0）回退 relay，并隔离失败候选直到 endpoint 变化；VPS 与 SDserver 客户端均已升级 | `go test ./...`、`go vet ./...`、`./scripts/project-audit.sh`；VPS↔SDserver 双向 0% loss、HTTP 20/20；SDserver→NAS `:8080` 20/20 |
