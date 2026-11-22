# Snet Client 文档

> 文档基线：客户端 `v1.2.2`。服务端、管理台和 relay 文档在独立的私有仓库，不在本仓库。

## 客户端

| 文档 | 用途 |
|---|---|
| [`../README.md`](../README.md) | 安装、平台入口、构建 |
| [`../deploy-prod/README-client.md`](../deploy-prod/README-client.md) | 客户端部署入口 |
| [`../deploy-prod/docker-client/README.md`](../deploy-prod/docker-client/README.md) | Docker / 群晖 NAS 客户端 |
| [`../deploy-prod/manual-client/README.md`](../deploy-prod/manual-client/README.md) | 二进制 + systemd 客户端 |
| [`MAC-DEV-NOTES.md`](MAC-DEV-NOTES.md) | macOS 客户端开发、签名与 LaunchDaemon |
| [`NETWORK-TROUBLESHOOTING.md`](NETWORK-TROUBLESHOOTING.md) | 直连、中继、NAT、路由排障 |
| [`ONLINE-STATUS-LOGIC.md`](ONLINE-STATUS-LOGIC.md) | 在线状态判定 |

## 硬件固件

| 文档 | 用途 |
|---|---|
| [`../scripts/hardware/README.md`](../scripts/hardware/README.md) | OpenWrt Image Builder、设备矩阵、刷机 |

## 发布

| 文档 | 用途 |
|---|---|
| [`RELEASING.md`](RELEASING.md) | 客户端与 shared 发布流程、tag、GHCR 与回滚 |

## 维护规则

- 结构变化（新包、端口、配置字段、平台契约）→ 同步更新 `README.md` 和本索引。
- 不把服务端部署、管理台、Cell、内部 RPC、私有 registry 或生产凭据写进本仓库。
- 过时但仍需保留的客户端文档放入 `archive/` 或 `docs/archive/`，不要留在当前文档目录。
