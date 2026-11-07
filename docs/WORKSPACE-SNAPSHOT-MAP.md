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

结论：当前可研发范围是 **Go 源码阅读/编辑/编译/测试（含 `-race`） + `shared/web` 编辑与外部 tsc 类型检查**；Web 打包、Android 与发布链路仍待补缺失目录/工具后再验证。
