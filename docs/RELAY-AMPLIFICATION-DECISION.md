# Relay 扇出放大与去重决策材料

> 日期：2026-11-07
> 范围：`internal/server/store.go`（`RelayRouteNodePort` / `deliver`）、`internal/server/relay.go`（`SendFanned`）、`internal/client/pathmanager.go`（relay shim）
> 依据：`docs/NETWORKING-ARCHITECTURE-REVIEW-2026-10-25.md` §4.3、§5、§7 T12、§8.4、§9 阶段 1
> 状态：已实施（2B、2B follow-up、1B+1C 计数、3C、3C legacy follow-up）。本文记录实施后状态。

## 1. 评审原文立场

### §4.3 relay（根本问题）

> - 用 `host:port` 推断节点身份，共享出口下天然歧义。
> - 扇出是概率性投递，会复制流量、污染 sibling，且可能完全漏掉真正接收方。
> - relay 帧没有节点 ID / session 认证，无法精确路由。
> - 单 relay、端口池、扇出放大，缺少带宽/队列/丢弃可观测性。

### §5 候选方案（方案 A）

> | A. 修补现有 relay | 路径对称、精确 flow、禁止错误扇出 | 改动小，能快速止血 | 仍是 host 身份 + 概率扇出 | 过渡 |

### §7 T12

> | T12 | relay 放大/重复 | tcpdump 计数 | 每包 ≤1 份有效副本，无错误 sibling 持续收包 | P1 |

### §8.4 验收标准

> relay：100 包 0% loss；每包最多一份有效副本。

### §9 阶段 1（过渡止血 A）

> - 共享出口不再向错误 sibling 盲扇出；无精确 flow 时明确丢弃并计数。

### 对"全局去重"的原文说明

评审**没有直接使用"全局去重"一词**。它的可验收表述是：

- §5 方案 A："禁止错误扇出"；
- §7 T12："每包 ≤1 份有效副本，无错误 sibling 持续收包"；
- §8.4："每包最多一份有效副本"；
- §9 阶段 1："不再向错误 sibling 盲扇出；无精确 flow 时明确丢弃并计数"。

因此，"全局去重"是达成上述目标的**可能实现手段**，不是评审原话。评审更明确反对的是"错误 sibling 扇出"和"放大"，而不是要求 relay 层对每个包做全局哈希去重。

## 2. "有效副本"分析

### relay 发送路径

| 路径 | 代码 | 发送粒度 |
|---|---|---|
| `deliver(sp)` exact / exclusive / shared | `store.go:1519-1558` | 每个匹配的 `host:port` flow 一份；`sentTo` 跨 sibling 共享，且只在成功时置位（2B + follow-up） |
| 共享 sender 候选循环 | `store.go:1578-1614` | 对每个 sibling 调用 `deliver(sp, sentTo)`；共享 `sentTo` 去重，失败不置位、允许下一 sibling 重试 |
| `relaySendFannedFrom` / `Relay.SendFanned` | `store.go:1058`、`relay.go:430-462` | 遍历 `p.seen` 后按 host 去重，选最近 seen 的 endpoint；每 host 一份（3C） |
| legacy `relayPair.handle` fan-out | `relay.go:350-367` | 同样按 host 去重，每 host 一份（3C follow-up） |
| `Relay.SendFrom` unicast | `relay.go:404-423` | 精确 `host:port` 一份；不做 host 去重 |

结论：广播路径（`SendFanned` 与 legacy `handle`）已按 host 去重，同 host N 个 flow 只收 1 份；unicast `SendFrom` 仍按 flow 精确发送，不做去重。

### 接收端 snetd

- Relay v2 shim 的 `inject` 直接把 payload 写入 WG 地址，**没有应用层去重**：
  - `internal/client/pathmanager.go:1461-1468`：
    ```go
    func (s *peerShim) inject(payload []byte) {
        if len(payload) == 0 {
            return
        }
        if _, err := s.conn.WriteToUDP(payload, s.pm.wgAddr); err != nil {
            s.pm.recvDrops.Add(1)
        }
    }
    ```
- 旧 relay 路径同样把 relay 作为 WG endpoint，relay 只转发不透明 UDP；snetd 不在 relay 层做包级去重。

### WireGuard 重放保护

WireGuard 协议自带重放保护：

- **数据包**：每包带 64 位 counter；接收端维护滑动重放窗口，同一 session 内重复 counter 会被丢弃。
- **握手包**：handshake initiation 带 timestamp（TAI64N），接收端拒绝不新于已见 timestamp 的重复/旧包；handshake response 也有 anti-replay 校验。
- **错误 sibling**：没有对应 peer 公钥/session，无法解密，直接丢弃。

因此：

- **同一 flow 的重复副本**：WG 层通常只处理一份，第二份在重放窗口被丢弃。
- **发到错误 sibling 的副本**：错误 sibling 无法解密，丢弃。
- **"有效副本"**：在 WG 层通常 ≤1；relay 层的 N 份不等于 N 次有效处理。

### 但"冗余无害"只成立于 WG 解密层

- relay/网络层仍然消耗带宽、CPU、队列；N 份副本会放大拥塞和延迟。
- 错误 sibling 仍会"持续收包"——这正是 T12 明确禁止的，即使它最终在 WG 层丢弃。
- 全局 packet-hash 去重有误伤风险：WG 握手重传、重钥、上层重传可能产生合法的新包；relay 层若按内容哈希去重，可能错误丢弃合法重传。**同一 flow 的重复交给 WG 重放保护更安全；relay 层应重点解决"错误 sibling"和"广播放大"。**

## 3. 决策选项

### 决策 1：广播回退放大（`SendFanned` 对每个 live flow 发一份）

| 选项 | 改动点 | 对 T12 的影响 | 延迟/带宽 | 风险 |
|---|---|---|---|---|
| **1A 保持现状**（未实施，被 1B/3C 取代） | 无 | 不满足"无错误 sibling 持续收包"；N 个 flow 收 N 份；WG 层有效副本仍 1 | 带宽/CPU 放大 N；拥塞时延迟上升 | 放大/污染持续；实现简单、兼容 |
| **1B 按 host 去重**（已实施：908fca7 + cf933a2，作为 3C） | `relay.go:437-452` 遍历 `p.seen` 时按 host 只选一个 endpoint | 减少同 host 多 flow 的副本；不同 host 的无关 sibling 仍会收到，不能完全满足 T12 | 降低同 host 放大；选错端口可能丢包 | 可能破坏 CGNAT 多出口/NAT 多映射；需要选端口策略 |
| **1C 禁止盲扇出**（已实施：404f382 + ff80f67，drop+count） | `store.go:1612/1618/1624` 的 fallback 改为只向已知 recipient 候选/flow 发送；无候选则丢弃并计数 | 最接近 §9 阶段 1 与 T12；不再向错误 sibling 盲扇出 | 带宽最小；首次握手可能因无候选而失败/延迟 | 可能牺牲 bootstrap/首次连通性；需要指标、重试或其他发现机制 |

### 决策 2：跨 sibling port 同一 recipient flow 重复发送

| 选项 | 改动点 | 对 T12 的影响 | 延迟/带宽 | 风险 |
|---|---|---|---|---|
| **2A 保持现状**（未实施） | 无；`sentTo` 仍是每个 `deliver(sp)` 独立（`store.go:1519`） | 同一 flow 在 B/C 两个 port 各发一份；WG 层有效副本 1 | 该 flow 2 倍带宽；可能增加队列延迟 | 放大；但保留多路径冗余 |
| **2B 共享 `sentTo` 跨 sibling**（已实施：9a33382 + 6652f87） | 把 `sentTo` 提升到候选循环作用域（`store.go:1578-1614`），或让 `deliver` 接受外部 map | 同一 flow 最多 1 份，直接满足"每包 ≤1 份有效副本" | 降低带宽；若第一份发送失败/路径不对称，失去冗余 | 需要失败重试/下一候选；共享 map 仍在 `s.mu` 下，锁风险低 |
| **2C 全局/短窗口去重**（未实施） | relay 层新增 flow+packet hash/TTL 缓存，`SendFrom`/`SendFanned` 前检查 | 最严格；跨 sibling、跨路径去重 | 带宽最小；CPU/内存增加 | 可能误丢合法重传/握手重试；缓存并发/TTL 复杂 |

### 决策 3：全局去重策略（relay 层 vs 依赖 WG）

| 选项 | 改动点 | 对 T12 的影响 | 延迟/带宽 | 风险 |
|---|---|---|---|---|
| **3A 依赖 WG 重放保护**（未实施） | 无；relay 不做全局去重 | WG 层"有效副本"≤1；但 relay 层放大、错误 sibling 收包仍存在 | 放大成本保留 | 不满足"无错误 sibling 持续收包"；但不会误丢合法重传 |
| **3B relay 层全局去重**（未实施） | 新增 flow+packet hash/短 TTL 缓存；所有发送路径先查重 | 最接近 T12；每 flow 每包最多一份 | 带宽最小；CPU/内存/哈希成本 | 误丢合法重传（握手/重钥）、缓存一致性、并发/TTL |
| **3C 混合：只对广播/多 flow 去重**（已实施：908fca7 + cf933a2） | 在 `SendFanned` 和 shared-host fan-out 路径做 flow/host 去重；exact/unicast 交给 WG | 降低广播放大和跨 sibling 重复；同 flow 重复由 WG 处理 | 折中；实现中等 | 路径间行为不一致；需要清晰语义和测试 |

## 4. 倾向（待用户决策）

以下仅为建议，**最终由用户决策**：

- **决策 1：倾向 1C**。它最贴合评审 §5"禁止错误扇出"与 §9 阶段 1"无精确 flow 时明确丢弃并计数"。若担心首次握手可达性，可先用 **1B** 作为过渡，但 1B 仍不能解决不同 host 的无关 sibling。
- **决策 2：倾向 2B**。同一 recipient flow 的重复对 WG 没有收益，共享 `sentTo` 改动小、直接满足 T12；需要补"第一份失败后尝试下一候选"的测试。
- **决策 3：倾向 3C**。relay 层重点处理广播/多 flow 放大；同一 flow 的重复交给 WG 重放保护，避免全局 packet-hash 误丢合法重传。

**已落地**：2B（`9a33382` + `6652f87`）、3C（`908fca7` + `cf933a2`）、1B/1C 的计数与收窄（`404f382`）、全失败 drop+count（`ff80f67`）。全失败行为详见 `RELAY-FAILURE-DECISION.md` §8。

## 5. 实施后的副本数事实

| 场景 | 当前副本数 | 依据 |
|---|---:|---|
| 广播对同 host N 个 live flow | **1** | `SendFanned` 与 legacy `handle` 均按 host 去重，选最近 seen 的 endpoint |
| 跨 sibling port 同一 recipient flow | **1**（成功后才去重） | 共享 `sentTo`；失败不置位，下一 sibling 可重试 |
| 同一 `deliver(sp)` 内重复 `host:port` | **1** | `sentTo` 单次内去重 |
| unicast `SendFrom` 精确 flow | 每目标 1 份，**不做 host 去重** | `Relay.SendFrom` 未改 |
| 所有 sibling 发送都失败 | **0 份 + 计数** | decision B（`ff80f67`）：有候选但全失败 → drop+count，不广播；无候选 bootstrap 才广播 |

补充：
- 广播回退的 host 去重选择“最近 seen”的 endpoint；CGNAT 同 host 多映射存在选错端口/可达性下降的风险。
- `relayPair.handle` 的 legacy broadcast 也已按 host 去重；3C 不再只覆盖 `SendFanned`。
- WireGuard 重放保护仍会丢弃重复密文；relay 层去重主要降低带宽/CPU/错误 sibling 收包，而不是防止 WG 重复处理。
