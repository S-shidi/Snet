# SNET 决策日志

> 按时间顺序记录已落地的产品/架构决策。只记录决策与证据，不替代设计文档。
> 基线：`3fb8b97`（快照导入，上游 `c379790` 不可比）。
>
> 注意：第 2、13 条已被 v1.2.0 取代——仓库不再收缩为 server + snetd + web，
> Android 已恢复为交付目标，桌面端已升级到 Wails v3。以 `DESIGN.md` /
> `PROJECT-ATLAS.md` 和当前代码为准。

## 1. 2026-11-07：采用快照导入基线

- 背景：工作区是 server + snetd + web 的收缩快照，上游完整仓库不可访问。
- 选项：A. 以快照为唯一基线；B. 等待上游 `c379790` 后比对。
- 选择：A。
- 理由：上游不可比；先把当前快照的代码、测试、缺口固定下来，后续再补上游数据。
- 对应 commit：`3fb8b97`。
- 影响：`verified_code_commit` 以快照为起点；上游差异保持“不可比”，不写成已验证。

## 2. 2026-11-07：收缩为 server + snetd + web

- 背景：Android、Docker 部署、CI、发布产物和过期文档不在本快照可维护范围。
- 选项：A. 保留完整仓库；B. 收缩为 server + snetd + web core。
- 选择：B。
- 理由：减少不可验证目录和过期事实；把精力集中在可编译/可测试的 Go 与 Web 核心。
- 对应 commit：`9e1c763`、`50a51a4`、`58691dc`、`688a967`、`0dc27db`、`5ae3ea3`、`d124a1f`、`f98878a`。
- 影响：VERSION、Android、snetbind、CI、Docker/nginx、发布产物成为永久缺口；见 `docs/archive/WORKSPACE-SNAPSHOT-MAP.md`。（2026-11-21 更正：这些缺口已随完整仓库恢复而关闭。）

## 3. 2026-11-07：exact-flow 发送失败删除 hint（方案 A）

- 背景：exact flow 存在但发送失败时，旧代码仍标记 `sentTo`，失败映射继续吞包，形成静默丢包。
- 选项：A. 失败即删 hint，允许重新归因；B. 广播回退；C. 引入 TTL/隔离状态。
- 选择：A。
- 理由：最小 diff，直接解决 stale hint 持续吞包；广播回退会放大并触碰 T12。
- 对应 commit：`de0e52c`（红测试）、`a9b9163`（修复）、`0b58b03`（文档）。
- 影响：失败后下一次调用可重新归因；接受短时 hint churn。

## 4. 2026-11-07：跨 sibling 共享 `sentTo`（2B）+ 成功才置位

- 背景：同一 recipient flow 在不同 sibling node port 各发一份，造成重复/放大。
- 选项：2A 保持每 port 独立 `sentTo`；2B 跨 sibling 共享；2C relay 层全局去重。
- 选择：2B。
- 理由：直接满足 T12“每包 ≤1 份有效副本”，改动小；不引入全局哈希/TTL。
- 对应 commit：`9a33382`、`6652f87`（follow-up：只有发送成功才置位，允许下一候选重试）。
- 影响：同 flow 最多一份；第一份失败时继续尝试下一 sibling。

## 5. 2026-11-07：收窄共享扇出 + 保留 bootstrap 兜底 + 计数（1B+1C）

- 背景：共享出口无精确 flow 时，旧代码向所有 live flow 盲扇出，错误 sibling 持续收包。
- 选项：1A 保持现状；1B 按 host 去重；1C 禁止盲扇出、无候选丢弃并计数。
- 选择：1B + 1C。
- 理由：最接近评审 §9 阶段 1“不向错误 sibling 盲扇出”；保留 bootstrap 首次连通性，并增加丢弃计数。
- 对应 commit：`404f382`（1B+1C）。
- 影响：减少无关 sibling 收包；无候选时可能牺牲首次握手，依赖计数/重试观察。

## 6. 2026-11-07：广播按 host 去重（3C，含 legacy broadcast）

- 背景：同一 host 的多个 flow 会在广播/共享扇出中收到多份副本。
- 选项：3A 依赖 WG 重放保护；3B relay 层全局去重；3C 只对广播/多 flow 去重。
- 选择：3C。
- 理由：降低广播放大，同时避免全局去重误丢合法重传。
- 对应 commit：`908fca7`、`cf933a2`（legacy pair broadcast follow-up）。
- 影响：同 host 最多一份；不同 host 的无关 sibling 仍可能收到，T12 仅部分满足。

## 7. 2026-11-07：所有 sibling 发送失败时 drop + count（决策 B）

- 背景：有候选但全部发送失败时，继续广播会重新引入放大和错误 sibling 收包。
- 选项：A. 回退广播；B. 丢弃并计数；C. 其他隔离/重试策略。
- 选择：B。
- 理由：与 §9“无精确 flow 时明确丢弃并计数”一致；避免重新放大。
- 对应 commit：`ff80f67`。
- 影响：全失败时帧被丢弃，但计数可观测；不向错误 sibling 重播。

## 8. 2026-11-07：删 hint 后 30s TTL 抑制广播（决策 B 补丁）

- 背景：exact-flow hint 删除后立即广播，会在短时间内重新引入放大。
- 选项：A. 立即允许广播；B. 删除后 TTL 内抑制广播。
- 选择：B（30s）。
- 理由：给重新归因留出窗口，同时避免瞬时广播风暴。
- 对应 commit：`36aa257`。
- 影响：短时间内可能不广播；超过 TTL 后恢复。

## 9. 2026-11-08：node-token 路由校验路径 `nid/nodeID`，不匹配 404（方案 B）

- 背景：`direct-endpoints`、`subnets`、`delete-node` 忽略路径参数；跨网络合法 token 返回 204。
- 选项：A. 保留现状并文档明确；B. 校验路径，不匹配 404；C. 移除路径参数。
- 选择：B。
- 理由：当前客户端始终传匹配值，不破坏现有调用；404 与 `ownerNID` 隐藏存在性语义一致；比 C 兼容性成本低。
- 对应 commit：`d46e38f`（红测试）、`9bf9682`（修复）、`d873163`（文档）。
- 影响：跨网络/错 nodeID 返回 404；匹配路径保持原成功码；旧客户端若传不匹配路径会开始失败。

## 10. 2026-11-08：pending poll 采用幂等轮询语义（200 + status）

- 背景：pending 已消费后第二次请求返回 200 + `status:"gone"`，而任务曾期望 404。
- 选项：A. 改为 404；B. 保留 200 + status，明确幂等语义。
- 选择：B（文档确认，未改代码）。
- 理由：`PendingClaim` 对“从未存在”和“已消费”都返回 `{Status:"gone"}, false, nil`；handler 均写 200，二者不可区分，无枚举信号；pending/approved/denied/gone 统一为 200 + status 更适合轮询。
- 对应 commit：`8b50a64`（文档）。
- 影响：客户端以 `status` 判断，不依赖 404；不泄露“曾存在”。

## 11. 2026-11-07：`markDirectFailed` 不清 `dp.addr`（保留快速恢复）

- 背景：direct 写失败后 `dp.ready=false`，但 `dp.addr` 保留；同来源 ACK 可快速恢复 ready。
- 选项：A. 清 `dp.addr`；B. 保留，依赖 ACK 重新证明可达。
- 选择：B（未改代码）。
- 理由：入站 ACK 是该来源可达的证据；清 addr 也不能阻止重新学习；保持快速恢复路径。
- 对应 commit：`00145db`（文档记录；无代码变更）。
- 影响：出站持续失败时可能反复“失败→ACK 恢复→再失败”；属于已知风险，未在本次修改。

## 12. 2026-11-07：`admin.html` 公开是设计

- 背景：`GET /admin` 无凭据返回 200；管理 API 才由 `requireAdmin` 保护。
- 选项：A. 页面本身鉴权；B. 页面公开，仅 API 鉴权。
- 选择：B。
- 理由：首次 bootstrap/login 需要页面公开；页面是静态 UI，不含服务端 secret；API 仍有 static token/session 保护。
- 对应 commit：`e1c87c1`（文档确认；`TestAdminWebRoot` 锁定公开行为）。
- 影响：admin.html 不作为鉴权缺口；不把页面公开误写成 API 公开。

## 13. 2026-11-08：放弃 Android 客户端，只保留 Docker Web 用于组网验证

- 背景：下一阶段主线是验证核心组网能力；Android/snetbind 构建链不在本快照，继续维护会分散验证重点。
- 选项：A. 保留 Android 客户端与 snetbind 待补；B. 放弃 Android，只保留 Docker Web 客户端 UI。
- 选择：B。
- 理由：Docker Web + snetd 是当前保留的客户端路径，组网验证以它为准；Android 不是当前交付目标。
- 对应 commit：`docs: drop android client, keep docker web for network validation`。
- 影响：`snetbind/`、`android/` 从“永久缺口”改为“明确移出范围”；CI 若保留仅用于 Go 测试/Web 构建；下一阶段集中做 Docker Web 多节点组网验证，见 `docs/archive/NETWORK-VALIDATION-PLAN.md`。（2026-11-21 更正：Android 已恢复为交付目标，见第 14 条。）

## 14. 2026-11-21：Android 恢复为交付目标

- 背景：完整仓库恢复后，Android 客户端、`snetbind`、签名与发布流程已重新纳入维护范围。
- 选项：A. 继续只保留 Docker Web；B. 恢复 Android 客户端并保持单网络模式。
- 选择：B。
- 理由：移动端是主要使用场景；Android 已有 gomobile 桥、VPN Service、扫码和审批 UI。
- 影响：撤销第 13 条。Android 为单网络模式；桌面端仍支持多网络并行。

## 15. 2026-11-21：拆分 SnetServ 与 Admin Server

- 背景：管理台与公网协调 API 混在同一进程，管理员凭据和服务令牌边界不清。
- 选项：A. 继续单进程；B. 拆分 `cmd/snetserv` + `cmd/admin-server`，管理台通过 loopback RPC 访问数据。
- 选择：B。
- 理由：公网 API 不再暴露 `/admin`；内部 RPC 仅 loopback + 服务令牌；`admin-server` 只暴露登录 session。
- 影响：保留 `cmd/server` 作为兼容入口；新部署优先使用拆分入口。`admin-server` 当前只管理一个 SnetServ。

## 16. 2026-11-21：文档与目录整理

- 背景：`DESIGN.md` / `PROJECT-ATLAS.md` 仍描述 Tauri、固定服务器、32 个 gomobile 方法等旧实现；根目录混有旧客户端和快照文档。
- 选项：A. 继续增量修补；B. 重写核心文档，归档旧客户端与快照文档，保留当前文档索引。
- 选择：B。
- 理由：减少误导性事实；当前文档只描述可验证的代码行为；历史内容仍可追溯。
- 影响：新增 `docs/README.md`、`docs/archive/`、`archive/`；旧 Tauri 客户端移至 `archive/desktop-mac-tauri/`；发布脚本补充 `snetserv` / `admin-server`。

## 17. 2026-11-21：删除 cmd/server，主服务端统一为 snetserv + admin-server

- 背景：v1.2.x 保留 `cmd/server` 作为单进程兼容入口，导致管理面与公网 API 仍可能混在一起，联邦化 Phase 0 后主 VPS 仍运行旧入口。
- 选项：A. 继续保留兼容入口；B. 删除 `cmd/server`，主服务端只运行 `cmd/snetserv`，管理台独立为 `cmd/admin-server`。
- 选择：B。
- 理由：管理面彻底剥离，公网 `:8090/admin` 返回 404；TLS、内部 RPC、健康接口统一由 snetserv 提供；发布产物和维护面收敛。
- 影响：`cmd/server/` 删除；Compose 改为 `snetserv + admin-server + relay`；管理台迁移到 `:8095`；旧 systemd 文档移入 `deploy/archive/`。
