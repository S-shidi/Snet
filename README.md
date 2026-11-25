<div align="center">

# Snet

**建立一个独属于你的私人或办公网络，无论你在何处，你的网络始终都在线**

P2P 直连 + 按需中继 · macOS / Windows / Android / NAS / iOS

[![License](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/S-shidi/Snet?display_name=tag)](https://github.com/S-shidi/Snet/releases/latest)
[![Tests](https://github.com/S-shidi/Snet/actions/workflows/ci.yml/badge.svg)](https://github.com/S-shidi/Snet/actions/workflows/ci.yml)

[![Download macOS](https://img.shields.io/badge/Download-macOS-000000?style=for-the-badge&logo=apple&logoColor=white)](https://github.com/S-shidi/Snet/releases/latest)
[![Download Windows](https://img.shields.io/badge/Download-Windows-0078D4?style=for-the-badge&logo=windows&logoColor=white)](https://github.com/S-shidi/Snet/releases/latest)
[![Download Android](https://img.shields.io/badge/Download-Android-3DDC84?style=for-the-badge&logo=android&logoColor=white)](https://github.com/S-shidi/Snet/releases/latest)
[![Docker](https://img.shields.io/badge/Docker-NAS%20%2F%20Linux-2496ED?style=for-the-badge&logo=docker&logoColor=white)](deploy-prod/docker-client/README.md)

</div>

<div align="center">
  <img src="docs/assets/web-console.png" alt="Snet 控制台" width="100%" />
  <p><sub>Snet 控制台：网络状态、设备在线情况、直连 / 中继路径、公网地址与子网路由</sub></p>
</div>

## 为什么是 Snet

Snet 是一个开箱即用的私有网络。离开公司或家之后，你仍然可以访问公司与家里的网络和服务。Snet 把你的多台设备加入同一个虚拟网络，使用体验接近本地局域网。

- **全平台**：macOS、Windows、Android、Linux / Docker / 群晖 NAS；桌面端支持多网络并行，Android 采用稳定的单网络模式。
- **P2P 优先，按需中继**：LAN、IPv6、NAT 打洞优先；同 LAN 直连失败时自动尝试公网 hairpin，最后回退 Relay v2。
- **路径与地址可见**：成员卡片展示真实公网地址、直连 / 中继状态、NAT 类型和子网路由。
- **成员连通性测试**：在网络详情对任意成员执行「测试」，通过隧道内 UDP 测试包报告响应速度与丢包，不依赖系统 ICMP。
- **标准 WireGuard 接入**：一键生成 `.conf` 和二维码，手机、软路由、任何标准 WireGuard 客户端都能直接导入。
- **官方 Hub / 原生网关**：无需改造现有 LAN，通过 Hub 或委派路由访问内网网段。

> 本仓库只包含客户端和硬件固件。服务端、管理台和 relay 是独立私有组件，不在此仓库发布；客户端通过邀请链接加入已有网络。

## 安装客户端

- **macOS（Apple Silicon）**：从 [Latest Release](https://github.com/S-shidi/Snet/releases/latest) 下载 `.dmg`。
- **Windows**：从 [Latest Release](https://github.com/S-shidi/Snet/releases/latest) 下载 `Snet-v*-win-Setup.zip`，解压后运行安装程序。
- **Android**：从 [Latest Release](https://github.com/S-shidi/Snet/releases/latest) 下载 APK。当前为单网络模式。
- **NAS / Linux**：[Docker 客户端部署](deploy-prod/docker-client/README.md)。

客户端首次启动后打开 Web 控制台，设置管理密码，然后通过邀请链接加入网络。在「设置 → 关于与更新」中可检查最新版本并一键升级（macOS 自动挂载 `.dmg` 替换应用并重启，Windows 静默运行 NSIS 安装器并重启，Android 自动下载 `.apk` 并调起系统安装器，Docker / Linux Web 端展示宿主机 `docker compose pull && docker compose up -d` 一键复制命令）。

## Docker / NAS

```bash
mkdir -p /opt/snet
cd /opt/snet
curl -fLO https://raw.githubusercontent.com/S-shidi/Snet/master/deploy-prod/docker-client/compose.yaml
docker compose up -d
```

默认镜像：

```text
ghcr.io/s-shidi/snet-client:latest
```

镜像 tag 统一为 `vX.Y.Z`（与 Git tag 相同，带 `v` 前缀），`latest` 跟随最新稳定版。生产环境建议固定版本，例如 `ghcr.io/s-shidi/snet-client:v1.2.7`。自 `v1.2.4` 起不再提供无 `v` 前缀的镜像 tag，旧 compose 里的 `snet-client:1.2.3` 需改为 `snet-client:v1.2.7`。

Web 控制台：

```text
http://<主机IP>:8081
```

完整说明见 [`deploy-prod/docker-client/README.md`](deploy-prod/docker-client/README.md)。

## 硬件固件

OpenWrt 固件构建在 [`scripts/hardware/`](scripts/hardware/README.md)：

```bash
bash scripts/hardware/build-firmware.sh --list
bash scripts/hardware/build-firmware.sh --device NanoPi-R5C
bash scripts/hardware/build-firmware.sh --all
```

固件会注入 `snetd`、`snetctl`、共享 Web UI、procd 服务以及预置域名 `snet.lan`（插上网线，在浏览器输入 `http://snet.lan` 即可访问 SNET 控制台；如果 `snet.lan` 无法访问，请检查你的主路由器 DNS 设置，或手动使用设备 IP 访问）。

## 从源码构建

要求 Go 1.26、Node.js 20。

```bash
go test ./...
go build ./cmd/client/...
./scripts/build-web.sh
./scripts/build-client-release.sh
```

构建产物在 `dist/`。

Android：

```bash
./scripts/build-android-web.sh
```

macOS / Windows 的完整打包流程见 [`docs/MAC-DEV-NOTES.md`](docs/MAC-DEV-NOTES.md) 和 `.github/workflows/`。

## 文档

- [客户端部署入口](deploy-prod/README-client.md)
- [Docker 客户端 / NAS](deploy-prod/docker-client/README.md)
- [二进制 + systemd 客户端](deploy-prod/manual-client/README.md)
- [硬件固件](scripts/hardware/README.md)
- [网络排障](docs/NETWORK-TROUBLESHOOTING.md)
- [在线状态逻辑](docs/ONLINE-STATUS-LOGIC.md)
- [文档索引](docs/README.md)

## 安全

不要在 issue、PR 或聊天中粘贴真实 `.env`、私钥、token、二维码或导出的 WireGuard 配置。安全报告请使用 GitHub Security Advisories，见 [`SECURITY.md`](SECURITY.md)。

## 许可证

客户端组件使用 **BSD 3-Clause**。服务端、管理台和 relay 是独立私有组件，不在本仓库中；它们不适用本仓库的 BSD 许可证。详见 [`LICENSE`](LICENSE) 和 [`COMMERCIAL.md`](COMMERCIAL.md)。
