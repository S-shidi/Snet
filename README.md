# SNET 虚拟组网

端到端加密的私有虚拟局域网（WireGuard 数据面 + HTTPS 协调 + UDP 中继）。

- **服务端**（Go）：网络协调、成员管理、NAT 穿透探测、UDP 中继（懒绑定，按需端口）、Web 管理页（分页列表、总览统计）、设备授权码（支持过期/续期/吊销）、设备命名。安全加固：限流、安全头（CSP/HSTS/no-store）、XFF 最右、凭据加锁、引导互斥、auth-status 需设备令牌。协议/API 版本协商（不匹配回 426）。
- **客户端**（Go daemon + Tauri 桌面端）：macOS（launchd）、Windows（SCM 服务）、Linux 均支持；
  数据面优先 NAT 打洞直连（公网站点做 ±1..±8 候选盲投），打不通时经服务器中继转发；启用 Relay v2 时改用 signed ticket + `127.0.0.1` shim 的节点 ID 精确转发，并经 relay 辅助发现尝试 UDP 直连、失败自动回退；直连按同 LAN > 全局 IPv6 > relay 观测公网映射择优，支持多 relay 同时在线/故障切换与控制帧限流。`/ctl/status` 的 `relayPath` 暴露 `directReady/directPeers`、`relayCandidates/relayReady/relayIndex/natType` 与逐 peer `peerDirect.path/kind/addr/lastRecvSec`，旧路径继续用 `peerPaths`。设备名自动上报（首次连接填充，管理端改名优先）。支持密钥定期轮换。协调服务器地址固定为 `https://snet.uizhi.eu.org:8090`；有 `deviceToken` 的设备每次启动先查授权状态并同步历史网络，授权码过期时客户端置冻结态、保持网络离线并显示过期提示。
- **Android 端**（Compose 原生 + gomobile AAR）：内置 VPN 服务，扫码/链接加入网络。设备名取 manufacturer+model。
- **Docker 客户端**：支持在 Docker 主机、群晖 NAS 等成员主机容器化部署；VPS 当前仅运行服务端。
- **手机**：服务器端生成节点配置，用 WireGuard App 导入（见 `deploy/phone-android.conf` 模板）。

## 目录结构

```
cmd/                     Go 入口
  server/                服务端主程序
  client/snetd/          daemon（macOS launchd / Windows 服务 / Linux 前台）
  client/snetctl/        命令行管理工具
internal/
  buildinfo/             版本/commit/build date（发布时由 ldflags 注入）
  server/                HTTP API、中继、探测、管理页（admin.html 经 go:embed 内嵌）
  client/                daemon 核心：隧道（ifconfig/netsh）、设备 ID、ctl 接口
  protocol/              客户端-服务端协议类型
deploy/                  部署文档、systemd 服务单元、手机配置
  docker/                Docker 部署配置（Dockerfile、entrypoint、nginx、compose）
    web/                 Docker 端 web 资产（由 build-web.sh 生成）
shared/web/              共享 TypeScript UI 源码（Docker/桌面端用；Android 不使用）
                         ui.ts、types.ts、utils.ts、subnet.ts、index.html、styles.css
snetbind/                gomobile 绑定层（SetDeviceName、ensureDaemon）
scripts/                 构建、部署、e2e 测试脚本
  build-web.sh           统一 web 资产构建脚本（Docker/桌面端共享）
  build-release.sh       生成版本化发布产物 + checksums
  bump-version.sh        同步所有组件版本并递增 Android versionCode
  check-version.sh       校验 VERSION 与各组件一致
  project-audit.sh       结构漂移审计（包/路由/skill/图链接/mode）
build/                   各平台构建输出（由脚本生成，未提交）
dist/                    发布产物（由 scripts/build-release.sh 生成，未提交）
VERSION                  产品版本（发布 tag 必须与它一致）
.github/workflows/       CI、Windows/Android 构建、tag 发布
```

## 架构图

可交互 HTML（自包含，浏览器直接打开）：

- [产品架构总览](.archify/architecture-snet-product-20261020-162140/snet-product-architecture.html)
- [用户全链路：安装→设备授权→建网/入网→组网](.archify/workflow-snet-user-lifecycle-20261022-143213/snet-user-lifecycle.html)
- [入网与审批生命周期](.archify/architecture-snet-lifecycle-20261020/snet-join-lifecycle.html)
- [NAT 直连/中继回退时序](.archify/architecture-snet-nat-path-20261020/snet-nat-path.html)
- [bbolt 数据模型 ERD](.archify/architecture-snet-bbolt-erd-20261020/snet-bbolt-erd.html)

详见 [`DESIGN.md`](DESIGN.md)。跨模块接手、全局约束与验证入口见
[`docs/PROJECT-ATLAS.md`](docs/PROJECT-ATLAS.md)。版本与发布流程见
[`docs/RELEASE.md`](docs/RELEASE.md)，各环境当前版本见
[`docs/DEPLOYMENTS.md`](docs/DEPLOYMENTS.md)，版本历史见
[`CHANGELOG.md`](CHANGELOG.md)。当前四节点组网合理性与测试矩阵见
[`docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md`](docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md)。

## 构建

### Web 资产（Docker 控制台）

**重要**：修改 `shared/web/` 中的 TypeScript 源码后，必须运行此脚本同步 Docker 产物：

```bash
./scripts/build-web.sh
```

此脚本会：
- 从 `deploy/docker/web-build/web.ts` 编译 Docker `app.js` → `deploy/docker/web/`
- 同步 `index.html` + `styles.css` 到该平台

> Android 客户端为 Compose 原生实现，不依赖 `shared/web`（见 `docs/ANDROID.md`）。

### Go（本机任意平台，交叉编译零依赖）

```bash
# 服务端 + 客户端（本机）
go build ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl

# Linux（VPS 部署）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/linux-amd64/... ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl

# Windows daemon（mac 上交叉编译；安装包在 Windows 机器或 CI 构建）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/windows-amd64/snetd.exe ./cmd/client/snetd
```

### 桌面端（Tauri）

```bash
cd desktop && npm install
npm run tauri dev        # 开发
npm run tauri build      # macOS 出 .app/.dmg
npm run tauri build -- --bundles nsis   # Windows 出安装包（须在 Windows 机器，或走 CI）
```

### Docker 端（无需本机编译环境）

```bash
docker build -t snet-web:latest .
```
## 测试

```bash
go test ./internal/server ./internal/client ./internal/protocol   # Go 单元/e2e 测试
```

## 部署

### 服务端部署

见 [`deploy/README.md`](deploy/README.md)。

### Docker 客户端部署

支持在 Docker 主机、群晖 NAS 等成员主机容器化部署客户端；VPS 当前只运行服务端。

**Docker 主机要点**：
1. 客户端 WG 端口必须避开服务端 relay 池（51820-52075，懒绑定按需占用）
2. 使用 `--network host` + `--cap-add NET_ADMIN` + `--device /dev/net/tun`
3. 客户端 WG 端口配置在 relay 池之外（如 52100）

**构建 Docker 镜像**：
```bash
VERSION=$(cat VERSION)
docker build -f deploy/docker/Dockerfile \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t "snetd:$VERSION" .
```

**运行 Docker 客户端**：
```bash
docker run -d --name snetd --network host \
  --cap-add NET_ADMIN --cap-add SYS_MODULE \
  --device /dev/net/tun:/dev/net/tun \
  -v /opt/snet/data:/data \
  -v /etc/letsencrypt:/host-certs:ro \
  --restart unless-stopped \
  snetd:1.1.0
```

### Windows 安装包

手动触发 `.github/workflows/build-windows.yml`（Actions），或推送 `v*` tag
由 `release.yml` 自动构建；安装包在 `snet-windows-nsis` artifact 中。
本地构建需准备 `build/windows-amd64` 下的 `snetd.exe`、`wintun.dll`、`LICENSE-wintun.txt`
（CI 会自动下载并校验 Wintun 0.14.1），详见 [`deploy/README.md`](deploy/README.md) §8。

### 服务器 rollout

```bash
./scripts/build-release.sh          # 生成 dist/ 版本化二进制 + checksums
SNET_ADMIN_USER=... SNET_ADMIN_PASSWORD='...' ./scripts/rollout-vps.sh
```

`rollout-vps.sh` 会备份旧二进制/数据库、原子替换、校验版本与 `/healthz`，失败自动回滚。
完整发布/回滚流程见 [`docs/RELEASE.md`](docs/RELEASE.md)。

## 重要注意事项

1. **子网路由**：客户端可通过控制 API 设置 `allowedSubnets` 广播子网路由，需启用 IP 转发
2. **设备 ID**：
   - Android：硬件绑定（Build.* + ANDROID_ID → SHA-256），仅硬件变更时改变
   - 其他平台：文件持久化（`device.id`），支持重装恢复
3. **端口规划**：
   - 服务端：8090（HTTPS API）、8091（探测）、51820-52075（relay 池，懒绑定）、8092（Relay v2）、8094（relay metrics，默认仅 loopback）
   - 客户端 Docker：52100+（WG 端口，避免与服务端 relay 池冲突）；Relay v2 直连另用临时 UDP 端口，bridge 模式需 host network/DNAT 才能同 LAN 直连
4. **配置文件路径**：
   - macOS：`/usr/local/snet/daemon.json`
   - Docker：`/data/daemon.json`（通过 volume 持久化）
5. **死锁修复**：`hostname()` 不能在持有 `d.mu.Lock()` 时调用，需使用 `hostnameLocked()` 变体
6. **自动端口映射**：默认尝试 UPnP-IGD / NAT-PMP / PCP，为活跃 WireGuard 端口建立临时映射；仅在网关 WAN IP 与探测公网 IP 一致时向对端广播。桌面/Web/Android 设置页可开关并查看映射状态，安全敏感部署也可在 `daemon.json` 设 `"natMapping": false`
