# SNET 文档索引

> 文档基线：`v1.2.1`（2026-11-21）。
> 当前行为以代码为准；历史文档只作背景，不作为实现依据。

## 当前文档

| 文档 | 用途 |
|---|---|
| [`../README.md`](../README.md) | 安装、构建、部署入口 |
| [`../DESIGN.md`](../DESIGN.md) | 系统设计：架构、业务流、数据模型、API、端口 |
| [`PROJECT-ATLAS.md`](PROJECT-ATLAS.md) | 项目全局索引、目录地图、不变量、构建矩阵 |
| [`DEPLOYMENTS.md`](DEPLOYMENTS.md) | 生产拓扑、主机与路径 |
| [`DECISION-LOG.md`](DECISION-LOG.md) | 已落地的产品/架构决策 |
| [`NETWORK-TROUBLESHOOTING.md`](NETWORK-TROUBLESHOOTING.md) | 网络排障：直连、中继、NAT、路由 |
| [`ONLINE-STATUS-LOGIC.md`](ONLINE-STATUS-LOGIC.md) | 在线状态判定逻辑 |
| [`RELAY-V2-CUTOVER.md`](RELAY-V2-CUTOVER.md) | Relay v2 切换与回滚 runbook |
| [`MAC-DEV-NOTES.md`](MAC-DEV-NOTES.md) | macOS 客户端开发、签名与 LaunchDaemon |
| [`releases/v1.2.1.md`](releases/v1.2.1.md) | v1.2.1 发布说明与升级顺序 |
| [`releases/v1.2.0.md`](releases/v1.2.0.md) | v1.2.0 历史发布说明 |
| [`assets/web-console.png`](assets/web-console.png) | Web 控制台截图 |

部署相关：

- [`../deploy-prod/docker-server/README.md`](../deploy-prod/docker-server/README.md)：VPS 服务端 + Relay v2
- [`../deploy-prod/docker-client/README.md`](../deploy-prod/docker-client/README.md)：Docker / NAS 客户端
- [`../deploy-prod/manual-client/README.md`](../deploy-prod/manual-client/README.md)：二进制 + systemd 客户端
- [`../deploy/README.md`](../deploy/README.md)：传统 systemd 部署（历史，当前推荐 `deploy-prod/`）
- [`../deploy/archive/`](../deploy/archive/)：旧 NAS/Docker/排障文档

## 历史文档（已归档）

以下文档描述的是收缩快照期或旧版网络行为，保留用于追溯，不保证与当前代码一致：

- [`archive/WORKSPACE-SNAPSHOT-MAP.md`](archive/WORKSPACE-SNAPSHOT-MAP.md)
- [`archive/FRESH-DEPLOY-PROBE.md`](archive/FRESH-DEPLOY-PROBE.md)
- [`archive/NETWORK-VALIDATION-PLAN.md`](archive/NETWORK-VALIDATION-PLAN.md)
- [`archive/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md`](archive/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md)
- [`archive/RELAY-FAILURE-DECISION.md`](archive/RELAY-FAILURE-DECISION.md)
- [`archive/RELAY-AMPLIFICATION-DECISION.md`](archive/RELAY-AMPLIFICATION-DECISION.md)

历史客户端与产物：

- [`../archive/desktop-mac-tauri/`](../archive/desktop-mac-tauri/)：旧 Tauri macOS 客户端
- [`../archive/snet-mac-fix.tar.gz`](../archive/snet-mac-fix.tar.gz)：历史修复包

## 文档维护

- 结构变化（新包、路由、端口、bucket、配置字段、平台契约）→ 同步更新 `DESIGN.md` 和 `PROJECT-ATLAS.md`。
- 过时但仍然有背景价值的文档 → 移入 `docs/archive/`，不要留在当前文档目录。
- 不要在文档里写真实 token、私钥、`.env` 或生产凭据。
