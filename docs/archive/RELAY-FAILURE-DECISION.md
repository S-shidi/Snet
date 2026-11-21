# Relay 失败行为决策矩阵

> 日期：2026-11-07
> 范围：`internal/server/store.go` 的 `RelayRouteNodePort` / `deliver` 闭包
> 依据：`NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md` §3.1、§7 T12、§8.4；`../PROJECT-ATLAS.md` §3.2
> 状态：决策材料；方案 A 已实施（commit `a9b9163`）。

## 1. 评审原文与解释边界

评审 §3.1 的结论原文：

> **结论：不是普通丢包，是路径不对称 + relay 收件人 flow 缺失导致的单向黑洞。**

§3.1 第 4-6 步原文：

> relay 在 VPS 的 `52018` socket 上找不到 SDserver 的 flow：
> `relayFlowByNode[KAYSC3GQ]=""`、`recipientFlows=map[]`、`recipientHosts=map[]`、
> `sharedHosts=map[39.180.206.133:true]`
>
> 只能退化为共享出口扇出；sender socket 上的 live flow 只有 NAS/phone：
> `39.180.206.133:2574`（NAS）、`39.180.206.133:2765`（phone）。
>
> 这些副本到不了 SDserver 的 `.2694`；SDserver 自然收不到。

T12 原文：

> 每包 ≤1 份有效副本，无错误 sibling 持续收包。

§8.4 原文：

> relay：100 包 0% loss；每包最多一份有效副本。

**解释边界**：§3.1 描述的是“收件人 flow 缺失”导致的单向黑洞，并未逐字规定“exact-flow 已存在但发送失败”时该删除、隔离还是广播回退。因此下表中：

- “评审明确”只用于 §3.1/§8.4 直接写到的内容；
- “推断”来自 §3.1 的根因（不能让失败映射继续吞包）与 T12/§8.4 的 0% loss / ≤1 有效副本目标；
- 无法从原文直接得出的，标“评审未明确”。

## 2. 决策矩阵

| 路径 | 失败时当前行为（代码位置） | 评审 §3.1 期望（原文/推断） | 差异 | 影响 | 判断 |
|---|---|---|---|---|---|
| **exact flow** | `store.go:1527-1532`：`ok := relaySendFrom(...)`；无论 `ok` 都 `sentTo[fl]=true`；不删除 `ns.relayFlowByNode[recipientID]` 或 `ns.flowByNode[fl]`。`store.go:1560-1562` 因 `sentTo` 非空返回 `true`，所以不会触发 `relaySendFannedFrom`。 | §3.1 只描述 flow **缺失**时的黑洞；未规定 flow **存在但发送失败**。按 T12/§8.4 的 0% loss 目标推断：失败候选应隔离，或回退到下一候选/广播，不能让帧静默丢失。原文引用：`relayFlowByNode[KAYSC3GQ]=""`、`recipientFlows=map[]` 导致“单向黑洞”。 | **评审未明确**（与 0% loss 目标存在张力）。当前行为既不删候选，也不广播回退。 | 失败映射可能已 stale；`relayFlowByNode`/`flowByNode` 保留到下一次 flow 刷新或 `pruneStaleRelayFlowsLocked`（`store.go:2049-2072`）。期间发往该 peer 的帧会被 `sentTo` 挡住回退，形成静默丢包；不会重复发送。 | **评审未明确**；偏“不一致”（相对推断目标） |
| **exclusive host** | `store.go:1538-1549`：`ok := relaySendFrom(...)`；`sentTo[fl]=true`；`if !ok { delete(recipientHosts, nh) }`（`:1546`）。同一 `deliver` 内后续同 host flow 不再尝试；不重试；`sentTo` 仍阻止广播回退。 | §3.1 未逐字规定 host 级删除；但其根因是“不要继续把帧发到失败/错误映射”。删除失败 host 与这一意图一致。原文引用：“这些副本到不了 SDserver 的 `.2694`；SDserver 自然收不到。” | **一致（方向）**；评审未明确到“删除 host 候选”这一实现细节。 | 同 host 的后续 flow 可能被跳过；若失败是瞬时错误，可能牺牲同 host 下其他有效 flow。无重复发送；仍无广播回退。 | **一致**（评审未逐字规定） |
| **shared host** | `store.go:1550-1558`：`ok := relaySendFrom(...)`；记录 `ok=false`；`sentTo[fl]=true`；**不删除** `recipientSharedHosts` 或 flow；`store.go:1560-1562` 返回 `true`，不触发广播回退。 | §3.1 把 shared-host fan-out 当作 recipient flow 缺失时的兜底；要求副本能到达 recipient 的真实映射（“recipient 的 NAT 接受匹配的副本”）。未规定 shared-host 单次发送失败后的处理。 | **评审未明确**；当前是 best-effort，无重试、无隔离、无进一步回退。 | 若所有 shared-host 副本都失败，帧丢失；若只有部分失败，仍可能成功。额外 sibling 副本依赖其 WG 会话丢弃；T12 的“无错误 sibling 持续收包”未被强制。 | **评审未明确** |

补充：`sentTo` 是每个 `deliver(sp)` 调用的局部 map（`store.go:1519`），因此跨 sibling node port 的同一 flow 不受同一个 `sentTo` 去重保护；这与 T12 的“每包 ≤1 份有效副本”相关，但本矩阵聚焦失败处理，不展开重复投递问题。

## 3. 如果要改：最小方案（均未实施）

### 方案 A：exact-flow 失败后删除 hint，等待重新归因

- **状态**：**已实施**（commit `a9b9163`）。
- **改动点**：`store.go:1527-1532` 的 exact-flow 分支，在 `ok == false` 时：
  - 删除 `ns.relayFlowByNode[recipientID]`；
  - 仅当 `ns.flowByNode[fl] == recipientID` 时删除 `ns.flowByNode[fl]`。
- **flowByNode 选择：删除**。理由：`recipientFlows` 由 `relayFlowByNode[recipientID]` 和 `flowByNode[owner == recipientID]` 共同构建（`store.go:1457-1464`）。只删 `relayFlowByNode` 时，`flowByNode[fl]` 会在下一次调用把同一个 dead flow 重新加回 `recipientFlows`，失败会重复；删除 recipient-owned 的 `flowByNode[fl]` 才能让下次真正重学。删除前校验 owner，避免误删 sibling 的 flow。
- **风险**：
  - UDP 发送失败可能是瞬时错误（缓冲区、临时网络），删除有效 hint 会让 `ListPeersFrom`/`OnRelayFlow` 重新学习，造成 churn。
  - 删除 `relayFlowByNode` 会同时影响 direct-punch hint，可能让 peer 暂时失去 relay 精确路径。
  - 删除 `flowByNode[fl]` 已加 owner 校验，避免误删 sibling 的 flow。
- **已加测试**：
  - `TestRelayRouteExactFlowFailureDeletesHintAndReattributes`：失败后 `relayFlowByNode`/`flowByNode` 被清空，第二次触发不再尝试 dead flow，走广播回退。
  - `TestRelayRouteExactFlowSuccessKeepsCandidate`：成功发送不删除。
  - 待补：`TestRelayRouteExactFlowFailureRelearnsOnNextFlow`（后续 `OnRelayFlow` 恢复）。

### 方案 B：exact-flow 失败后允许广播回退

- **改动点**：`store.go:1519` 增加 `sentOK`（或等价成功计数）；`store.go:1527-1532` 只有 `ok == true` 时才把该 flow 计为成功；`store.go:1560-1562` 改为“至少一个成功才返回 `true`”，全部失败返回 `false`，让 `RelayRouteNodePort` 在 `1612/1618/1624` 调用 `relaySendFannedFrom`。
- **风险**：
  - 广播会向该 relay socket 上所有 live endpoint 发送，可能让无关 sibling 收到副本，直接触碰 T12“无错误 sibling 持续收包”。
  - 若部分 flow 成功、部分失败，必须避免“成功后又广播”造成重复；需要 `sentOK` 而不是简单清空 `sentTo`。
  - 广播放大/重复风险高于方案 A。
- **需要的新测试**：
  - `TestRelayRouteExactFlowFailureFallsBackToBroadcast`：全部 exact flow 失败时 `SetRelaySendFanned` 被调用一次。
  - `TestRelayRoutePartialExactFlowSuccessDoesNotBroadcast`：任一成功则不广播。
  - `TestRelayRouteBroadcastFallbackDoesNotDuplicateSuccessfulFlow`：成功 flow 只收到一次。

### 方案 C：exact-flow 失败后隔离一段时间（类似 `badDirect`）

- **改动点**：
  - 在 `networkState` 增加 `relayFlowBadUntil map[string]time.Time`（键为 flow 或 `nodeID+flow`）。
  - `store.go:1457-1464` 构建 `recipientFlows` 时跳过未过期的 bad flow；`store.go:1527-1532` 失败时写入 `badUntil[fl] = now + TTL`。
  - 成功发送或 `OnRelayFlow` 刷新（`store.go:1990-1995`）时清除隔离；`pruneStaleRelayFlowsLocked`（`store.go:2049-2072`）顺带清理过期项。
  - 隔离期间让该 peer 走 shared-host 扇出/其他候选。
- **风险**：
  - 新增内存状态、TTL、锁内清理；需要防止 map 增长和锁竞争。
  - 隔离时间过长会延长 relay 回退；过短则可能反复试错。
  - 若 shared-host 也失败，仍然会丢包；隔离只是避免持续打 stale exact flow。
  - 重启后隔离状态丢失（内存态），但可接受。
- **需要的新测试**：
  - `TestRelayRouteExactFlowFailureIsolatesFlow`：失败后下一次调用跳过 exact，走 shared-host 扇出。
  - `TestRelayRouteExactFlowIsolationExpires`：TTL 到期后重新尝试 exact。
  - `TestRelayRouteExactFlowSuccessClearsIsolation`：成功发送清除隔离。
  - `TestRelayRouteExactFlowIsolationDoesNotAffectSibling`：隔离仅作用于该 recipient/flow。

## 4. 方案对比与倾向

| 维度 | 方案 A（删 hint） | 方案 B（广播回退） | 方案 C（TTL 隔离） |
|---|---|---|---|
| 改动量 | 最小 | 小 | 中（新增状态/清理） |
| 失败后是否继续打 stale flow | 否（hint 被删，等待重学） | 否（改走广播） | 否（TTL 内跳过） |
| 是否触发广播 | 否 | 是 | 否（走 shared-host/其他候选） |
| T12“无错误 sibling 持续收包”风险 | 低 | **高** | 低-中 |
| 瞬时错误导致误删/误隔离 | 高（立即删） | 低 | 中（TTL 可调） |
| 重复发送风险 | 低 | 中-高 | 低-中 |
| 需要的测试量 | 少 | 中 | 多 |

**原倾向：方案 C**。它直接针对“stale exact flow 持续吞包”，同时避免方案 B 的广播放大和错误 sibling 收包；代价是需要新增 TTL 状态与清理逻辑。若要求最小 diff，**方案 A** 次之，但必须接受瞬时失败导致 hint 被删和重新归因 churn。**不建议默认采用方案 B**：它与 T12 的“无错误 sibling 持续收包”目标冲突最大。

**实际落地：先实施方案 A（`a9b9163`）**，作为最小止血；方案 C 仍保留为后续增强候选。

## 5. 结论

- **exclusive host**：当前行为方向一致（失败 host 不再重试），但评审未逐字规定。
- **exact flow**：已按方案 A 实施（`a9b9163`）：失败后删除 `relayFlowByNode` 与 recipient-owned `flowByNode`，下一次不再重试 dead flow，可重新归因/回退。评审 §3.1 未明确该场景，本次按最小止血落地。
- **shared host**：当前行为是 best-effort；评审未明确失败后的处理，未改。
- 本次实施只动 exact-flow 分支；exclusive-host、shared-host、广播回退、`sentTo` 未改。方案 C 仍为后续增强候选。

## 6. 广播回退副本放大（待决策）

**代码事实**：`Relay.SendFanned`（`internal/server/relay.go:430-462`）遍历 `p.seen`，键是 `host:port` live endpoint；对每个 endpoint 写一份数据报，排除 `except` 和 `ctrlOnly`。**不是按 host 去重，而是按 live flow 逐条发送。**

**量化**：`TestRelayRouteSharedHostDoesNotFanOutToUnrelatedSibling`（commit `1c37cb1`）的场景 2 给无关 sibling E 配 3 个 live flow（同一 host、不同端口），触发广播回退，测试记录：

```text
scenario 2: broadcast fallback sent 3 copies to unrelated sibling
[203.0.113.99:31003 203.0.113.99:31004 203.0.113.99:31005]
(one per live flow; known amplification, pending decision)
```

| E 的 live flow 数 | 广播回退时 E 收到副本数 |
|---:|---:|
| 0 | 0 |
| 1 | 1 |
| 3 | 3 |
| N | N |

**含义**：
- 场景 1（B/C 有精确 flow 命中）不触发广播，E 收到 0 份。
- 场景 2（recipient 无 flow）走广播回退，E 收到 N 份；这是 T12“每包 ≤1 份有效副本”的放大点。
- 当前只固化事实，未改 `SendFanned`/广播触发条件。是否收窄（按 host 去重、只发给 recipient 候选、或限制每包副本数）待决策。

**关联**：评审 §4.3“扇出是概率性投递，会复制流量、污染 sibling”；T12“每包 ≤1 份有效副本，无错误 sibling 持续收包”。

## 7. 跨 sibling port 同一 recipient flow 重复发送（待决策）

**代码事实**：`sentTo` 在 `deliver(sp)` 内部创建（`store.go:1519`），每个 sibling 的 `deliver(sp)` 各有一份；共享 sender 的候选循环（`store.go:1578-1614`）对 B/C 分别调用 `deliver`，不共享 `sentTo`。

**量化**：`TestRelayRouteDeduplicatesSameRecipientFlowAcrossSiblings`（commit `846daa6`）：

- 同一 `Rf` 同时出现在 B 和 C 的 portflows → `Rf` 被发送 **2 次**。
- 同一 `deliver(sp)` 内 portflows 有重复 `host:port` → 只发送 **1 次**（`sentTo` 单次内去重）。

| 场景 | 副本数 |
|---|---:|
| 同一 flow 出现在两个 sibling port | 2 |
| 同一 flow 在一个 port 的 flow 列表重复 | 1 |

**含义**：跨 sibling port 没有去重；这是 T12“每包 ≤1 份有效副本”的另一个放大点。当前只固化事实，未改 `sentTo` 作用域、`deliver` 或候选循环；是否收窄待决策。

## 8. 2B follow-up + decision B：成功才置位与全失败 drop+count

**实施**：

- `store.go` 三处 `sentTo[fl] = true` 改为仅在 `ok == true` 时置位（commit `6652f87`）。
- 全失败时改为 drop+count，不再广播（commit `ff80f67`，decision B）。

**行为变更**：

- 旧行为：exact-flow 失败 → `sentTo[fl]=true` → `deliver` 返回 true → 不广播。
- 2B follow-up：失败 → 不置位 → 所有 sibling 都失败 → `deliver` 返回 false。
- decision B：`deliver` 返回 false 且 `attempted == true`（有候选 flow/endpoint，但全部发送失败）→ 递增 `relayFanoutFallbacks`，**不广播**，`RelayRouteNodePort` 返回 false。
- 仅当 `attempted == false`（recipient 从未有 flow/endpoint，bootstrap 无候选）时才保留 `relaySendFannedFrom` 广播。
- exact-flow 失败仍按 decision A 删除 `relayFlowByNode` / recipient-owned `flowByNode`。

**评审依据**：

> §9 阶段 1：“共享出口不再向错误 sibling 盲扇出；无精确 flow 时明确丢弃并计数。”

decision B 把“有候选 flow 但全部失败”也按 §9 的“丢弃并计数”处理，避免重新引入错误 sibling 扇出和放大；bootstrap 无候选场景保留广播以维持首次连通性。

**当前状态**：已实施（`ff80f67`）。`RelayRouteNodePort` 在全失败时返回 false；`RelayFanoutFallbacks()` 计数递增；不再广播。

### TTL 补丁（36aa257）

- 新增 `networkState.relayFlowBadUntil map[string]time.Time`，随 `networkState` 初始化。
- 常量 `relayFlowBadTTL = 30 * time.Second`。
- exact-flow 失败删除 hint 时：`relayFlowBadUntil[recipientID] = time.Now().Add(relayFlowBadTTL)`。
- 广播判定：`!attempted` 且 TTL 未过期 → `relayFanoutFallbacks++`，不广播，返回 false；TTL 过期后恢复 bootstrap 广播。
- `pruneStaleRelayFlowsLocked` 顺带清理过期 `relayFlowBadUntil`，避免泄漏。
- 评审依据：§9“无精确 flow 时明确丢弃并计数”；TTL 避免删 hint 后立刻退化为广播。

**生命周期与调用方核验**：

- TTL map 随 `networkState` 生命周期释放；`DeleteNetwork`/`SweepZombies` 会删除 `s.networks[nid]`，map 随之回收。
- `Join` 复用 `networkState`；`reviveNode` 现在清除该节点的 `relayFlowBadUntil`（commit `991a6a0`），避免旧 TTL 误抑制重新加入节点的广播。
- 节点移除路径可能把该节点 ID 的 TTL 条目保留到下一次 prune；节点 ID 随机且重新加入会创建新 ID，因此不会误抑制，网络删除时随 `networkState` 释放。
- `deliver` 二元返回所有调用点语义已核：`store.go:1658` 与 `store.go:1684` 都显式拆分 `sent`/`attempted`，没有把 `attempted` 误当 `sent`。
