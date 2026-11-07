注：Android 与真机部署已移出本工作区；§3.1/§5 relay 根因、§7 relay 测试项仍适用。

# SNET 四节点组网合理性与最佳方案评审

**状态**: 草案，待架构决策  
**评审时间**: 2026-10-25  
**评审对象**: `W4NSYT6E` / `10.88.1.0/24`  
**取代**: `docs/VIRTUAL-NETWORK-ARCHITECTURE-REVIEW.md`（2026-09-24，三节点旧拓扑）  
**边界**: 本文只做评审与测试设计；不修改代码、配置或部署。

## 0. 结论摘要

控制面功能基本完整，数据面组网当前不合理。

- 当前 relay 是“源 `host:port` 归因 + 共享出口扇出”的过渡方案，不是可靠的精确路由。
- 已出现单向黑洞：`SDserver → VPS` direct 0% 丢包，但 `VPS → SDserver` relay 100% 丢包。
- 同 LAN 节点没有走 LAN 直连：phone/NAS/SDserver 大量依赖 relay，`SDserver → NAS` 仍约 523ms。
- Android 无条件注入 `10.0.0.0/8 → tun0`，把本机 `10.7.86.0/24` LAN 也吸进隧道。
- 控制面 `peerPaths` 与数据面实际路径不一致；atlas 中的“双向 0% loss”与当前实测冲突。

**推荐长期目标：直连优先 + 带节点 ID 的 relay session 兜底 + 网络实际 CIDR 路由。**  
即 B（direct-first）+ C（relay shim）；A（修补现有 relay）只作为迁移期止血。

## 1. 当前拓扑

| 节点 | ID | Overlay IP | 本地/WG 端点 | relay 端口 | 备注 |
|---|---|---|---:|---:|---|
| VPS | `CJEB5UBZ` | `10.88.1.2` | `66.187.6.46:52100` | `52018` | 协调 + relay |
| SDserver | `KAYSC3GQ` | `10.88.1.4` | `10.7.86.222:52100` | `51840` | 家庭 LAN，Docker |
| phone | `GMUYDKB8` | `10.88.1.7` | `10.7.86.12:51820` | `51822` | 另有 `wlan1=10.7.86.20` |
| NAS | `FZJG72CR` | `10.88.1.8` | `10.7.86.111:51820` | `51823` | 家庭 LAN，Docker |

家庭公网出口为 `39.180.206.133`。抓包推断的共享出口 NAT 映射：NAS `:2574`、SDserver `:2694`、phone `:2765`。该映射会随 NAT 状态变化，只代表本次观测窗口。

## 2. 实测矩阵

| 方向 | 实际路径 | 丢包 | RTT | 数据来源 |
|---|---|---:|---:|---|
| SDserver → VPS | direct | 0%（10/10） | ~255 ms | 前序 tcpdump，`52100 ↔ 39.180.206.133:2694` |
| VPS → SDserver | relay 尝试 | **100%（0/10）**，此前 70% | — | 本轮 VPS ping |
| VPS → NAS | relay | 0%（10/10） | 305 ms | 本轮 VPS ping |
| VPS → phone | relay | 10%（9/10） | 349 ms | 本轮 VPS ping |
| SDserver → NAS | relay | 0%（10/10） | 523 ms | 前序实测 |
| SDserver → phone | relay | 0%（10/10） | 507 ms | 前序实测 |
| phone → SDserver/NAS/VPS | relay | 历史 0%（60 包） | — | 旧日志，待复测 |

当前 VPS `peerPaths` 的关键项：

- `KAYSC3GQ`：`mode=relay`、`relay=66.187.6.46:51820`、`direct=|39.180.206.133:52100`
- `KAYSC3GQ` 的 `peerStats.Endpoint=66.187.6.46:51840`
- `FZJG72CR`、`GMUYDKB8` 也均为 `mode=relay`

phone 日志显示三个 peer 端点全部是 relay：VPS `52018`、NAS `51823`、SDserver `51840`。

## 3. 关键证据与根因

### 3.1 VPS → SDserver 单向黑洞

1. SDserver 到 VPS 走 direct，10/10 请求与回复成功。
2. SDserver 因此不会向 VPS 的 relay 端口 `52018` 建立 flow。
3. VPS 仍把 SDserver 当 relay peer，向 `51840` 发送。
4. relay 在 VPS 的 `52018` socket 上找不到 SDserver 的 flow：
   - `relayFlowByNode[KAYSC3GQ]=""`
   - `recipientFlows=map[]`
   - `recipientHosts=map[]`
   - `sharedHosts=map[39.180.206.133:true]`
5. 只能退化为共享出口扇出；sender socket 上的 live flow 只有 NAS/phone：
   - `39.180.206.133:2574`（NAS）
   - `39.180.206.133:2765`（phone）
6. 这些副本到不了 SDserver 的 `.2694`；SDserver 自然收不到。
7. 偶发 direct 包会成功，所以历史上出现过 3/10，而不是稳定 0%。

**结论：不是普通丢包，是路径不对称 + relay 收件人 flow 缺失导致的单向黑洞。**

### 3.2 direct 候选与实际映射不一致

VPS 对 SDserver 的 direct 候选是 `39.180.206.133:52100`（自报/发布端口），但 SDserver 到 VPS 的实际观测映射是 `39.180.206.133:2694`。

- `.52100` 没有可用的入站端口映射时，VPS 无法主动打通 SDserver。
- `.2694` 是 SDserver 主动访问 VPS 52100 时建立的映射；VPS 的回包可以走它，但当前路径状态没有把它作为 direct 锁定目标。
- relay 也学不到 `.2694`，因为 SDserver 不向 relay 发数据。

**结论：观测端点没有进入路径决策；“自报端点”不能替代“实时观测映射”。**

### 3.3 控制面状态不是数据面真相

- VPS 说 `KAYSC3GQ` 是 relay，但数据面存在 direct 回包。
- atlas 说 `VPS↔SDserver` 双向 0%，当前实测单向 100%。
- `peerPaths` 是客户端状态机输出，不是双向端到端探测结果。

**结论：验收必须以双向数据面探测为准，控制面状态只用于排障。**

### 3.4 Android LAN 路由冲突

- `RoutePlanner` 固定 `MESH=10.0.0.0/8`。
- `10.7.86.0/24` 位于 `10/8` 内，当前代码直接跳过 exclude，并给出“无法同时保留”警告。
- phone 的 table `1131` 有 `10.0.0.0/8 dev tun0`，只有 `throw 66.187.6.46`，没有 `10.7.86.0/24` 例外。
- `detectLocalSubnets` 当前返回 `"null"`，Kotlin `JSONArray("null")` 抛异常，进一步导致 localSubnets 为空。

**结论：即使 relay 正常，phone 访问同 LAN 的 NAS/SDserver 也会被吸进隧道，无法本地直连。**

## 4. 合理性评估

### 4.1 控制面

**较好**

- 创建、加入、审批、设备绑定、密钥轮换、多网络、子网通告齐全。
- `/ctl/*` 与协调 API 分层清晰。
- relay 端口池、per-node 端口、备 relay 协议已有雏形。

**不足**

- `/peers` 未交换双方当前路径模式，两侧独立决策，允许一侧 direct、一侧 relay。
- 未交换 WG socket 的实时观测端点；自报端点与实际 NAT 映射脱节。
- 无双向 path truth、无路径收敛确认。
- 无 relay session、NAT 类型、能力位等明确契约。

### 4.2 数据面

**不合理点**

- 路径状态机是每 peer、每侧独立的，缺少对称性协商。
- relay 假设“接收方一定在发送方 relay 端口上有 flow”，一侧 direct 时不成立。
- direct 候选缺少双向打洞、路径验证和失败后的快速收敛。
- 同 LAN 没有优先级保障，`10/8` 路由和容器命名空间破坏本地直连。
- 公共 IP hairpin 被禁用是对的，但没有替代的 LAN 直连路径。

### 4.3 relay

**优点**

- 单播端口池、共享出口多映射扇出、CGNAT 兜底，能覆盖部分家庭网络。
- relay 不解密 WG，数据面加密边界正确。
- 已有备 relay 协议与 RTT 选优。

**根本问题**

- 用 `host:port` 推断节点身份，共享出口下天然歧义。
- 扇出是概率性投递，会复制流量、污染 sibling，且可能完全漏掉真正接收方。
- relay 帧没有节点 ID / session 认证，无法精确路由。
- 单 relay、端口池、扇出放大，缺少带宽/队列/丢弃可观测性。
- `whoami`/`group` 控制无认证，`group` 会向任意发送者列出 peer 端点。

### 4.4 路由与容器

- Android 的 `10/8` blanket route 是设计缺陷；本机 LAN 在 `10/8` 时必然冲突。
- NAS/SDserver 在 Docker 命名空间内，同 LAN 宿主服务需要 host network 或显式 DNAT。
- `SNET_LOCAL_IP` 只解决自报端点，不解决容器到宿主 LAN 服务的可达性。

### 4.5 安全与运维

- 本轮读取 phone `daemon.json` 时输出了 `privateKey`/`token`，应按已暴露处理并轮换。
- relay 控制面无认证；建议 session token/限流，`group` 不应对任意源返回完整 peer 列表。
- 缺少端到端 latency/loss 矩阵、relay 扇出计数、路径切换原因、重复包统计。
- atlas/旧评审与实测冲突，说明数据面回归没有进入发布门禁。

## 5. 候选方案与评分

| 方案 | 核心改动 | 优点 | 局限 | 定位 |
|---|---|---|---|---|
| A. 修补现有 relay | 路径对称、精确 flow、禁止错误扇出 | 改动小，能快速止血 | 仍是 host 身份 + 概率扇出 | 过渡 |
| B. 直连优先 | 交换 LAN/IPv6/观测映射，主动打洞，健康检查 | 延迟/带宽收益最大 | 对称 NAT 仍失败 | 必做 |
| C. Relay 节点 ID shim | 帧头带 sender/recipient ID，持久 session，精确转发 | 彻底解决共享出口；支持多 relay/HA | 协议/MTU/兼容成本高 | 长期核心 |
| D. Headscale/Tailscale | 成熟控制面 + DERP | 成熟、跨平台 | 产品模型/审批/绑定被重构 | 不优先 |

初评评分（1–5，权重为建议值，待矩阵验证）：

| 维度 | 权重 | A | B | C |
|---|---:|---:|---:|---:|
| 正确性 | 30 | 2 | 3 | 5 |
| NAT 鲁棒性 | 20 | 2 | 3 | 5 |
| 路径对称 | 15 | 3 | 4 | 5 |
| 同 LAN 本地性 | 10 | 1 | 5 | 3 |
| relay 效率 | 10 | 2 | 3 | 5 |
| 故障恢复 | 5 | 2 | 3 | 4 |
| 可观测性 | 5 | 2 | 3 | 4 |
| 兼容/成本 | 5 | 5 | 3 | 2 |
| **加权初评** | 100 | **2.20** | **3.35** | **4.55** |

**初评：C 是 relay 正确性最佳，B 是性能最佳；推荐 B+C，A 仅过渡。**

## 6. 推荐目标架构

```text
控制面 HTTPS /peers：
  每个 peer 下发：
    LAN 端点、IPv6、WG socket 观测映射、NAT 类型
    relay session ID、路径偏好、能力位

数据面优先级：
  同 LAN direct
  > 公网 IPv6 direct
  > 观测映射 + 双向打洞 direct
  > relay session（节点 ID 精确转发）

路径管理：
  双方路径协商，禁止一侧 direct、一侧 relay 的单向黑洞
  direct 健康检查 + 失败回退 + 周期重试
  relay 健康检查 + 多 relay 选优

路由：
  只注入本网络 CIDR + peer allowedSubnets
  不再无条件注入 10.0.0.0/8
  本机 LAN 用更具体 exclude/route 保留
  容器节点同 LAN 宿主服务走 host network 或显式 DNAT
```

### relay shim 关键点

- 帧格式：`[magic][version][senderID][recipientID][WG payload]`。
- 节点维持持久 relay session；relay 按 `recipientID` 精确投递，不再按源 `host:port` 猜。
- session 带认证 token，支持限流；`group` 不再向任意源暴露 peer 列表。
- MTU 需下调（例如 WG 1420 → relay 1380），并做 1400B/1420B 回归。
- 保留 legacy per-node relay 端口，按能力位共存，分阶段迁移。

## 7. 可重复测试矩阵

### 7.1 基础连通与路径

| ID | 场景 | 操作 | 通过标准 | 优先级 |
|---|---|---|---|---|
| T1 | 同 LAN phone↔NAS/SDserver | 100 包 ping + HTTP | direct，<20ms，0% loss | P0 |
| T2 | 同公网出口 cross-sibling | phone/NAS/SDserver 两两 ping 100 包 | 精确投递，0% loss，不复制给无关 sibling | P0 |
| T3 | 不同公网 cone NAT | A↔B 双向 ping + iperf3 | direct，0% loss，RTT 符合物理路径 | P0 |
| T4 | 对称 NAT/CGNAT | phone↔VPS/NAS/SDserver | relay，0% loss，允许 direct 失败 | P0 |
| T5 | IPv6 可用 | 双栈节点互 ping | 优先 IPv6 direct，0% loss | P1 |
| T6 | 一侧 direct、一侧 relay | 强制 SDserver direct、VPS relay | 不得黑洞；两侧收敛到同一模式或 relay shim 可投递 | P0 |
| T7 | relay 端口故障 | block `52018/51840` | <30s 切备 relay 或回 direct | P1 |
| T8 | NAT rebinding | 切换 Wi-Fi/重建映射 | <60s 恢复，旧 flow 淘汰 | P1 |
| T9 | phone 息屏/后台 5 分钟 | 定时 ping | 隧道保持或 <60s 恢复 | P1 |
| T10 | 多网络 `10/8` 重叠 | 加入两个 `10.x` 网络 | 不误捕获本机 LAN，不路由冲突 | P0 |
| T11 | 吞吐/MTU | iperf3 TCP/UDP，1400B/1420B | 无分片/黑洞，loss <1% | P1 |
| T12 | relay 放大/重复 | tcpdump 计数 | 每包 ≤1 份有效副本，无错误 sibling 持续收包 | P1 |
| T13 | relay 控制面滥用 | 未认证 `whoami/group`、洪泛 | 有限流/鉴权，不泄露 peer 列表 | P2 |
| T14 | 凭据/日志 | 检查日志与 `/ctl/status` | 无 token/privateKey/pairing code | P0 |

### 7.2 每项采集

- `/ctl/status`：`peerPaths`、`peerQuality`、`peerStats`、`natMapping`、`localSubnets`。
- VPS tcpdump：`ens18` 上 `52100`、relay 端口、共享出口映射。
- relay 日志：`recipientFlows`、`recipientHosts`、`sharedHosts`、`portflows`、`UNICAST`。
- 节点路由：`ip rule`、`ip route show table <vpn>`、Android `dumpsys`。
- 指标：双向 loss、RTT p50/p95、handshake age、direct/relay 比例、重复副本、切换耗时。

### 7.3 采集命令示例

```bash
# VPS -> overlay 节点
docker exec snet-web ping -c 10 -i 0.5 -W 2 10.88.1.4
docker exec snet-web ping -c 10 -i 0.5 -W 2 10.88.1.8
docker exec snet-web ping -c 10 -i 0.5 -W 2 10.88.1.7

# relay / direct 抓包
tcpdump -ni ens18 'udp port 52100 or udp portrange 51820-52075'

# 控制面状态（token 从容器读取，不要回显）
TOKEN=$(docker exec snet-web cat /data/ctl-token)
docker exec snet-web curl -s -H "X-Ctl-Token: $TOKEN" \
  http://127.0.0.1:19432/ctl/status

# Android 路由
adb shell ip rule
adb shell ip route show table 1131
```

## 8. 验收标准

- 同 LAN：direct，<20ms，100 包 0% loss。
- 公网 direct：0% loss；RTT 符合物理路径。
- relay：100 包 0% loss；每包最多一份有效副本。
- 路径对称：双方 `peerPaths` 模式一致，或 relay shim 可证明双向投递。
- 故障切换：relay 端口故障 <30s 恢复；NAT rebinding <60s 恢复。
- 路由：本机 LAN 不被 `10/8` 捕获；多网络无冲突。
- 安全：日志/状态无 token/privateKey/pairing code；relay 控制面有限流/鉴权。
- 文档：atlas 与评审报告不再与实测冲突。

## 9. 迁移路线

### 阶段 0：安全与基线

- 轮换 phone WG 私钥/节点 token；检查 SDserver 历史泄露。
- 修正 atlas/旧评审中与实测冲突的结论。
- 建立 T1–T14 基线，输出当前矩阵。

### 阶段 1：过渡止血（A）

- 路径对称守卫：一侧 relay 时，另一侧不得长期 direct。
- 共享出口不再向错误 sibling 盲扇出；无精确 flow 时明确丢弃并计数。
- 修复 Android `nil -> []` 与 `10/8` 内 LAN exclude。
- 验收：T6 不再黑洞；T1 同 LAN direct；T2 0% loss。

### 阶段 2：直连优先（B）

- `/peers` 交换 WG socket 观测映射、LAN、IPv6、NAT 类型。
- 双向打洞 + 路径健康 + 失败回退 + 周期重试。
- 验收：T3/T5 direct 0% loss；T4 relay 0% loss；T6 路径对称。

### 阶段 3：relay 节点 ID shim（C）

- 持久 session、精确转发、认证、限流、多 relay。
- legacy 兼容层 + MTU 回归。
- 验收：T2/T4/T7/T12/T13 全部通过；relay 不再依赖 host 归因。

### 阶段 4：可观测性与 HA

- 每 peer 路径真相、relay 队列/丢弃/扇出计数、loss/latency 历史。
- 多 relay 选优、故障切换、容量水位。

## 10. 风险与待决

1. **是否接受 relay 协议升级（节点 ID shim）**：共享出口正确性的分水岭。
2. **是否允许把 Android 路由从 `10/8` 改为网络实际 CIDR**：同 LAN 直连的前提。
3. **同 LAN 直连是否为硬性产品要求**：若是，NAS/SDserver 需要 host network/DNAT 方案。
4. **目标规模**：节点数、并发、带宽、是否需要多 relay/HA。
5. **测试环境权限**：SDserver/NAS 需要可重复的 `/ctl` 与 tcpdump 采集；目前 NAS 仅 Synology MCP，SDserver SSH 受限。
6. **凭据暴露**：phone `privateKey`/`token` 已在本轮工具输出中出现，按已暴露处理。

## 11. 当前进展

- 2026-10-25：Android 同 LAN 路由 P1 修复已实现：
  - `DetectLocalSubnets` 的 nil 结果在 Go 侧归一化为 `[]`；Kotlin 侧兼容 `"null"`。
  - `RoutePlanner` 允许 `10/8` 内更具体的本机 LAN `excludeRoute`；拒绝覆盖 `10/8`、包含 overlay IP 或与 peer 子网重叠的排除。
  - 新增 4 个 `RoutePlannerTest`；`go test ./...`、`go vet ./...`、Android `testDebugUnitTest` 通过。
  - APK 已重建：`android/app-native/build/outputs/apk/debug/app-native-debug.apk`（45.5M）。
- 真机安装未完成：`adb connect 10.7.86.12:39903` 当前 Connection refused；T1/T10 待 ADB 恢复后验证。
- 2026-10-25 14:45 UTC 复测：SDserver `10.7.86.222` 已恢复；NAS 容器因 Docker DNS `127.0.0.11` 超时离线，重启后恢复并完成 runtime 升级。VPS→SDserver、VPS→NAS 各 20/20 0% loss，`peerPaths` 两侧均为 `mode=relay`，T2/T3 当前通过。此前 `VPS→SDserver` 100% loss 在节点离线时复现，恢复后未再复现；relay 黑洞只在路径模式不对称时成立，需要强制一侧 direct 才能稳定复现。
- 手机 ADB 曾通过 VPS 端口转发 `127.0.0.1:39481 -> 10.88.1.7:39481` 连上，APK 已 push 到 `/data/local/tmp/snet-debug.apk`；`pm install` 期间手机 VPN/ADB 掉线，安装未确认完成。当前手机再次离线，T1/T10 待手机 VPN 与无线调试恢复后重试。

## 附录 A：代码入口

- relay 路由与扇出：`internal/server/store.go`（`RelayRouteNodePort`、`deliver`）
- relay socket/control：`internal/server/relay.go`
- 客户端路径选择：`internal/client/daemon.go`（`resolvePeerEndpoints`）
- 协议字段：`internal/protocol/types.go`（`Node`、`PeersResp`）
- Android 路由：`android/app-native/src/main/kotlin/com/snet/app/RoutePlanner.kt`
- Android 路由读取：`android/app-native/src/main/kotlin/com/snet/app/SnetVpnService.kt`
- gomobile 子网检测：`snetbind/snetcore.go`
