# SNET 虚拟组网

SNET 是一个端到端加密的私有虚拟局域网：WireGuard 负责数据面，HTTPS 协调服务负责成员与网络状态，UDP Relay 负责在无法直连时保证连通性。

当前版本：**v1.0.4**

## 核心特性

- **WireGuard 加密数据面**：节点之间使用 WireGuard 隧道通信。
- **P2P 优先**：同 LAN、全局 IPv6、NAT 打洞直连优先，失败自动回退 Relay。
- **Relay v2**：signed ticket + 节点级 UDP 转发，支持多 relay 候选与故障切换。
- **Web 控制台**：网络详情、成员管理、路径监控、子网路由、设备授权。
- **Docker 客户端**：一个镜像包含 `snetd`、nginx 和 Web UI，适合 Docker 主机与群晖 NAS。
- **服务端管理台**：网络、节点、待审批、设备授权码和网络详情运维。
- **可观测性**：`/ctl/status` 暴露 `relayPath`、`peerDirect`、NAT 类型和路径状态。
- **安全默认值**：HTTPS、鉴权、限流、安全头、设备授权码和设备身份持久化。

## 架构简介

```text
客户端 A ── WireGuard ── 客户端 B
    │                         │
    └──── 直连 / Relay v2 ────┘
              │
       HTTPS 协调服务
       ├── 网络与成员
       ├── 设备授权
       ├── NAT 探测 :8091/udp
       └── Relay v2 :8092/udp
```

- `cmd/server`：协调服务器与管理控制台。
- `cmd/relay`：Relay v2 数据面。
- `cmd/client/snetd`：客户端 daemon。
- `cmd/client/snetctl`：本地控制命令。
- `shared/web`：客户端 Web UI 源码。
- `internal/client/pathmanager.go`：直连/Relay 路径选择与回退。

## Quick Start

### 1. 服务端 Docker Compose

前置：Linux 主机、Docker Compose、TLS 证书、8090/tcp、8091/udp、8092/udp 可用。

```bash
git clone <repo-url> /opt/stacks/snet
cd /opt/stacks/snet
./scripts/build-release.sh

mkdir -p dist-deploy/linux-amd64
cp dist/server-linux-amd64 dist-deploy/linux-amd64/server
cp dist/relay-linux-amd64 dist-deploy/linux-amd64/relay
cp dist/snetd-linux-amd64 dist-deploy/linux-amd64/snetd

cd deploy-prod/docker-server
cp .env.example .env
# 编辑 .env，设置 SNET_RELAY_SECRET 和 SNET_RELAY_V2_ENDPOINTS
docker compose --env-file .env up -d --build
```

验证：

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
```

预期返回 `serverVersion: "1.0.4"` 和 `status: "ok"`。

### 2. 客户端 Docker 镜像

客户端镜像已发布到 VPS Registry：

```text
66.187.6.46:5000/snet-client:latest
```

在 Docker 主机或群晖 NAS 上：

```bash
docker login 66.187.6.46:5000 -u snet

# 标准 Docker 主机
cd /opt/stacks/snet
docker compose pull
docker compose up -d

# 群晖 NAS
cd /volume1/docker/snet
docker compose pull
docker compose up -d
```

打开：

```text
http://<主机IP>:8081
```

客户端容器使用 host network、`/dev/net/tun` 和 `/etc/snet` 数据卷；首次启动会在数据卷内生成稳定的 `device.id`。

### 3. macOS Wails 客户端

GitHub Actions 会构建并上传：

```text
SnetMac-v<VERSION>-<SHORT_SHA>.zip
SnetMac-v<VERSION>-<SHORT_SHA>.dmg
```

下载后打开 `.dmg`，把 `SnetMac.app` 拖入 Applications。

首次启动需要 macOS 管理员授权：客户端会通过 AppleScript 提权启动内置的 `snetd`，用于创建 `utun` 网卡、安装路由和写入：

```text
/usr/local/etc/snet/config.json
/usr/local/etc/snet/device.id
/usr/local/etc/snet/ctl-token
/usr/local/etc/snet/snetd.log
```

App 只使用包内 `Contents/Resources/snetd`，不依赖 `/usr/local/snet` 或系统 `PATH`。

本地构建见 [macOS Wails 客户端说明](desktop-mac/README.md)，开发排坑记录见 [MAC-DEV-NOTES](docs/MAC-DEV-NOTES.md)。

## 构建与发布

```bash
./scripts/build-release.sh
```

脚本会：

- 从 `VERSION` 读取版本号。
- 使用 `git describe --tags --always` 注入 commit。
- 使用 UTC 时间注入 buildDate。
- 交叉编译 Linux/Darwin 的 amd64/arm64 二进制到 `dist/`。
- 生成 `dist/checksums.txt`。

本地测试：

```bash
go test ./...
```

## 文档

- [VPS 部署](docs/VPS-DEPLOY-VIA-HOSTODO.md)
- [部署环境与路径](docs/DEPLOYMENTS.md)
- [项目总览](docs/PROJECT-ATLAS.md)
- [网络架构评审](docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md)
- [客户端 Docker 说明](deploy-prod/docker-client/README.md)
- [macOS Wails 客户端](desktop-mac/README.md)
- [macOS 开发排坑记录](docs/MAC-DEV-NOTES.md)

## 安全

- 不要提交 `.env`、`device.id`、`ctl-token`、registry 密码、私钥或手机 WireGuard 配置。
- 服务端 `.env` 与 registry 密码只保存在部署主机，权限保持 `0600`。
- 发布前运行 `git status` 和安全扫描，确认工作区干净且没有真实凭据。

## 网段冲突防护（v1.0.1）

- 客户端加入网络前会检查本机物理局域网与目标 CIDR 是否重叠，冲突直接拒绝；
  高级用户可用 `SNET_IGNORE_LAN_CONFLICT=1` 绕过。
- 路由安装失败不再静默：网络状态变为 `error`，Web UI 显示红色警告。
- 服务端自动网段池扩展到 `10.80.0.0/12`（4096 个 `/24`），按 CIDR 包含关系查重；
  显式重叠网段默认拒绝，可用 `allowOverlap: true` 放行。
- 详见 [客户端 Docker 说明](deploy-prod/docker-client/README.md#13-网段冲突防护v101)。
