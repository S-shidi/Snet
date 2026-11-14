<h1 align="center">SNET</h1>

<p align="center">
  端到端加密的私有虚拟局域网。WireGuard 负责数据面，HTTPS 协调服务负责成员与网络状态，UDP Relay 负责在无法直连时保证连通性。
</p>

<p align="center">
  当前版本：<b>v1.1.5</b> · <a href="https://github.com/S-shidi/Snet/releases/latest">下载最新版</a>
</p>

## 核心特性

- **WireGuard 加密数据面**：节点之间使用 WireGuard 隧道通信。
- **P2P 优先**：同 LAN、全局 IPv6、NAT 打洞直连优先，失败自动回退 Relay。
- **Relay v2**：signed ticket + 节点级 UDP 转发，支持多 relay 候选与故障切换。
- **委派子网路由**：网络创建者把 CIDR 委派给网关节点，其他成员自动通过该节点访问目标子网。
- **Web 控制台**：网络详情、成员管理、路径监控、子网路由、自定义 SnetServ。
- **Docker 客户端**：一个镜像包含 `snetd`、nginx 和 Web UI，适合 Linux 主机与群晖 NAS。
- **macOS 原生客户端**：DMG 安装，内置 LaunchDaemon、菜单栏状态与中继网关模式。
- **安全默认值**：HTTPS、鉴权、限流、安全头和设备身份持久化。

## 架构简介

```text
客户端 A ── WireGuard ── 客户端 B
    │                         │
    └──── 直连 / Relay v2 ────┘
              │
       HTTPS 协调服务
       ├── 网络与成员
       ├── 自定义 SnetServ
       ├── NAT 探测 :8091/udp
       └── Relay v2 :8092/udp
```

- `cmd/server`：协调服务器与管理控制台。
- `cmd/relay`：Relay v2 数据面。
- `cmd/client/snetd`：客户端 daemon。
- `cmd/client/snetctl`：本地控制命令。
- `shared/web`：客户端 Web UI 源码。
- `internal/client/routeplanner.go`：委派路由与直连/中继路径规划。

## Docker 部署

### 1. 部署服务端

前置条件：

- Linux VPS，Docker 与 Docker Compose。
- 一个域名，例如 `snet.example.com`，解析到 VPS。
- TLS 证书，默认示例使用 Let's Encrypt 路径。
- 放行 `8090/tcp`、`8091/udp`、`8092/udp`。

创建部署目录和 `.env`：

```bash
git clone https://github.com/S-shidi/Snet.git
cd Snet/deploy-prod/docker-server
cp .env.example .env
```

编辑 `.env`，至少设置：

```dotenv
SNET_RELAY_SECRET=用 openssl rand -hex 32 生成
SNET_RELAY_V2_ENDPOINTS=snet.example.com:8092
TLS_CERT_PATH=/etc/letsencrypt/live/snet.example.com/fullchain.pem
TLS_KEY_PATH=/etc/letsencrypt/live/snet.example.com/privkey.pem
```

启动：

```bash
docker compose --env-file .env up -d --build
```

验证：

```bash
curl -fsS https://snet.example.com:8090/healthz
```

预期：

```json
{"serverVersion":"1.1.5","status":"ok"}
```

如果不想从源码构建，可以直接使用预构建镜像：

```yaml
services:
  relay:
    image: crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-relay:v1.1.5
    container_name: snet-relay
    restart: unless-stopped
    environment:
      SNET_RELAY_SECRET: ${SNET_RELAY_SECRET}
    command: ["-addr", "0.0.0.0:8092", "-metrics-addr", "127.0.0.1:8094"]
    ports:
      - "8092:8092/udp"

  server:
    image: crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-server:v1.1.5
    container_name: snet-server
    restart: unless-stopped
    depends_on:
      - relay
    environment:
      SNET_RELAY_SECRET: ${SNET_RELAY_SECRET}
      SNET_RELAY_V2_ENDPOINTS: ${SNET_RELAY_V2_ENDPOINTS}
    command:
      - "-addr"
      - "0.0.0.0:8090"
      - "-probe-addr"
      - "0.0.0.0:8091"
      - "-db"
      - "/data/snet.db"
      - "-tls-cert"
      - "${TLS_CERT_PATH}"
      - "-tls-key"
      - "${TLS_KEY_PATH}"
    ports:
      - "8090:8090/tcp"
      - "8091:8091/udp"
    volumes:
      - /etc/letsencrypt:/etc/letsencrypt:ro
      - snet-data:/data

volumes:
  snet-data:
```

### 2. 部署 Docker 客户端 / NAS

客户端镜像使用阿里云 ACR 公开仓库，无需登录：

```text
crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.5
```

也可以直接拉取：

```bash
docker pull crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.5
```

标准 Linux 创建：

```yaml
# /opt/stacks/snet/compose.yaml
services:
  snetd:
    image: crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.5
    container_name: snetd
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - /etc/snet:/etc/snet
```

群晖 NAS 把数据卷改成：

```yaml
    volumes:
      - /volume1/docker/snet/etc:/etc/snet
```

启动：

```bash
cd /opt/stacks/snet       # 标准 Linux
# 或 cd /volume1/docker/snet   # 群晖 NAS
docker compose up -d
```

打开控制台：

```text
http://<主机IP>:8081
```

首次打开需要设置 Web 管理密码。更新客户端：

```bash
docker compose pull
docker compose up -d --force-recreate
docker exec snetd snetd -version
```

客户端容器需要 `/dev/net/tun` 和 `NET_ADMIN`；`network_mode: host` 可让 nginx、WireGuard 和本机控制 API 正常工作。

### 3. 连接自托管 SnetServ

默认连接官方 SnetServ。使用自建服务端时，在客户端 Web UI 或 macOS App 的「信息」页面：

1. 点击「协调服务」旁的修改按钮。
2. 填入 `https://snet.example.com:8090`。
3. 保存，客户端会先校验目标 `/healthz`。
4. 校验通过后自动重新连接；点击「恢复默认」可切回官方服务端。

## macOS 安装

系统要求：macOS 10.13+，建议使用 Apple Silicon Mac。

1. 打开最新 Release：

   <https://github.com/S-shidi/Snet/releases/latest>

2. 下载 `SnetMac-v1.1.5-*.dmg`。

3. 打开 DMG，把 `SnetMac.app` 拖入 `Applications`。

4. 第一次打开前，在「终端」执行：

   ```bash
   sudo xattr -cr /Applications/SnetMac.app && open /Applications/SnetMac.app
   ```

   输入 Mac 开机密码。该命令只需执行一次，用于解除未公证应用的隔离限制。

5. 首次启动会请求管理员授权，安装后台服务：

   ```text
   /Library/LaunchDaemons/com.snet.daemon.plist
   /usr/local/bin/snetd
   /usr/local/etc/snet/
   ```

6. 打开 App 后，在「信息」中设置 Web 管理密码，即可创建或加入网络。

7. 关闭窗口后 App 会留在菜单栏继续运行；彻底退出请从菜单栏选择「退出 SNET」。

macOS 客户端支持作为中继网关：在「信息」页面打开「允许本设备作为中继网关转发流量」。如果委派 CIDR 与本机物理局域网冲突，客户端会跳过该路由，并在网络状态中显示原因。

## 本地构建

```bash
./scripts/build-release.sh
```

脚本会读取 `VERSION`，交叉编译 Linux/Darwin amd64/arm64 的 `server`、`relay`、`snetd`、`snetctl`，输出到 `dist/` 并生成校验和。

运行测试：

```bash
go test ./...
```

## 文档

- [VPS 部署](docs/VPS-DEPLOY-VIA-HOSTODO.md)
- [部署环境与路径](docs/DEPLOYMENTS.md)
- [客户端 Docker 说明](deploy-prod/docker-client/README.md)
- [项目总览](docs/PROJECT-ATLAS.md)
- [网络架构评审](docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md)
- [macOS 开发排坑记录](docs/MAC-DEV-NOTES.md)

## 安全

- 不要提交 `.env`、`device.id`、`ctl-token`、Registry 密码、私钥或手机 WireGuard 配置。
- 服务端 `.env` 与 Registry 密码只保存在部署主机，权限保持 `0600`。
- 发布前运行 `git status` 和安全扫描，确认工作区干净且没有真实凭据。
