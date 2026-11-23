<!-- release-notes-v1.2.4 -->

## Breaking Change

从 `v1.2.4` 开始，客户端 Docker 镜像 Tag 统一为 `vX.Y.Z`（带 `v` 前缀，与 Git tag 相同）。
不再推送无 `v` 前缀的镜像 Tag。

请更新 compose 文件中的镜像地址：

```diff
- image: ghcr.io/s-shidi/snet-client:v1.2.3
+ image: ghcr.io/s-shidi/snet-client:v1.2.4
```

服务端镜像（`snet-snetserv`、`snet-admin-server`、`snet-relay`）同样只使用 `vX.Y.Z`。
`latest` 仅跟随最新稳定版；预发布 tag（如 `v1.2.4-rc1`）不会更新 `latest`。

## 首个拆分后的全量正式版

`Snet`（公开客户端/固件）与 `Snet-Server`（私有服务端）都已拆分完成，本次两端统一发布
`v1.2.4` / `server/v1.2.4`，shared 协议模块对齐 `shared/v1.2.4`。

## 直连与组网

- **Android IPv6 直连**：VPN 内允许原生 IPv6，从默认网络读取运营商全球 v6 并注入 daemon；
  有 v6 的对端（如 NAS）可无 NAT 直连。
- **移动 CGNAT 打洞**：对 IPv4 public 候选做 ±8 端口窗口 burst，并周期性使用 ±32 宽窗口；
  收到探测回 3 个 ack。
- **稳定性**：直连端口固定为 WireGuard 端口 +2/+3；Android TUN 重建只换 WireGuard 设备，
  保留 relay 会话与直连 socket；本机公网映射变化时立即重注册/重打洞。
- **快速收敛**：relay 注册 8s、未直连 lookup 5s；端点发布 3s 防抖；跳过离线 peer。
- 客户端与服务端镜像同时推送 **GHCR 与 ACR**，均为 `v1.2.4` + `latest`。

## 玩客云固件

- 本次硬件包**仅发布玩客云 S805**。
- 刷机后默认 Web 入口为 Snet：`https://<设备IP>/` → `/snet/`；OpenWrt/LuCI 已禁用，
  Snet 是唯一的 Web 控制台。
- 设备仍为 eth0 DHCP 客户端（主机名 `snet`），内置 DHCP 服务关闭。

## 产物

- `Snet-v1.2.4-binaries.tar.gz`（snetd/snetctl）
- `Snet-v1.2.4-wankecloud-s805.burn.img.xz`
- macOS DMG、Windows 安装包/zip、Android APK
- 容器镜像：`ghcr.io/s-shidi/snet-client:v1.2.4` 与 ACR 同名 tag
