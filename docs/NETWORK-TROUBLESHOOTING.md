# 网络排障笔记

本文记录 Relay v2 与 P2P 直连在实际网络环境中的已知陷阱，以及 `PathManager` 必须保持的行为约束。

## 网关 SNAT 导致 P2P 打洞包被丢弃

### 现象

- 对端（例如 `SDNAS`）的 `DIRECT_PROBE` 已成功穿过双方 NAT，抓包可在本机 `directConn` 端口看到。
- 但 `PathManager` 不回 `DIRECT_PROBE_ACK`，`peerDirect` 长期停留在 `relay` / `probing`，`recvDrops` 持续增长。
- 抓包中的源地址不是对端公网 IP，而是本机网关的 LAN 地址，例如：

```text
192.168.1.1:4405 -> 192.168.1.11:38704 typ=12(DIRECT_PROBE)
```

### 根因

部分路由器在入站 DNAT 转发时同时执行 LAN 口 Masquerade（SNAT）：

- 源 IP 被改写为网关 LAN IP（如 `192.168.1.1`）；
- 源端口通常原样保留（如 `4405`）。

`matchCandidateLocked` 若严格要求入站源 IP 等于对端 `public` 候选的 IP，就会把合法打洞包判为未知源并丢弃。此前的 `hintIP` 兜底也只在 IP 匹配时生效，无法覆盖网关改写。

### 修复约束

`matchCandidateLocked` 必须兼容网关改写，满足以下全部条件时可将该帧认定为 `public` 路径：

1. 候选类型是 `public`；
2. 源端口与候选端口一致；
3. 入站源 IP 属于本机直连网段（即本机网关所在 LAN）；
4. 该对端没有可用的 LAN 候选（避免把真正的同 LAN 流量误判为公网）。

不要把该逻辑放宽为“任意本机网段源地址都接受”：仅当端口一致且没有 LAN 候选时才接受，避免伪造/串流。

### 验证

- `sdserver` 日志应出现：

```text
path <nid>: DIRECT_PROBE established peer=<id> addr=192.168.1.1:<port> kind=public
```

- `/ctl/status` 的 `relayPath.peerDirect[<id>]` 应从 `relay` 变为 `direct`；
- `recvDrops` 不再因打洞包持续增长；
- 直连延迟应显著低于公网中继（实测从约 588 ms 降至约 6 ms）。

## selfPublicIP 残留导致 Cone NAT 被误判为 symmetric

### 现象

- NAT 映射端口长期稳定（例如始终为 `39.180.139.217:4405`），但 `/ctl/status` 与协调服务端都显示 `natType: "symmetric"`。
- `peerDirect` 中该节点始终无法升级直连。

### 根因

Relay v2 模式下 `probeLoop` 会跳过公网 IP 探测，`d.selfPub` / `NetworkCfg.PublicIP` 可能仍是上一次 PPPoE 拨号或旧网络环境下的公网 IP。

`noteDirectMapping` 先比较 `selfPublicIP` 与 relay 观测到的映射 IP；两者不一致时直接判定 `symmetric`，使后续“端口稳定 = cone”的判断永远无法执行。

### 修复约束

- Relay v2 模式下必须动态刷新本机观测公网 IP：
  - 优先使用协调服务端返回的 `Self.PublicIPv4`；
  - 缺失时使用 `PathManager.MappedPublicIP()`（relay 对 direct socket 的注册回显）；
  - 观测值写入 `d.selfPub[nid]` 并持久化到 `NetworkCfg.PublicIP`。
- `noteDirectMapping` 在连续两次映射完全一致时应判定 `cone` 并同步更新 `selfPublicIP`；
- 空的 `selfPublicIP` 不得覆盖已知观测值。

### 无全局 IPv6 时不要盲目发 IPv6 探测

没有全局 IPv6 地址的主机不应创建 `directConn6`，也不应把 `ipv6` 候选加入探测轮转；否则会持续产生 `sendDrops`，并挤占 IPv4 打洞窗口。`PathManager` 只有在存在全局 IPv6 时才应启用 IPv6 候选。

## 相关代码

- `internal/client/pathmanager.go`
  - `matchCandidateLocked`
  - `noteDirectMapping` / `MappedPublicIP`
  - `maintainDirect`
- `internal/client/daemon.go`
  - Relay v2 轮询中的 `selfIP` 刷新与 `NetworkCfg.PublicIP` 持久化
- `internal/client/daemon_test.go`
  - `TestSyncNetworksOnlyRemovesOnConfirmed404`
- `internal/client/pathmanager_test.go`
  - `TestPathManagerAcceptsGatewayMasqueradedPublicProbe`
  - `TestPathManagerStaleSelfPublicIPRecoversCone`

## 成员连通性测试

网络详情里的「测试」按钮由客户端守护进程执行，不使用系统 `ping`：

- 每个在线网络会在其虚拟 IP 的 UDP `19433` 端口启动一个回显监听器；
- 发起端从本机虚拟 IP 向目标成员发送 5 个探测包（间隔 90ms），按回复计算平均 RTT 与丢包率；
- 若对端是旧版本没有回显监听器，则自动回退到一次短时 ICMP 测试；
- 因此容器内没有 `CAP_NET_RAW`、或系统防火墙拦截 ICMP 时，只要两端都是当前版本，测试仍可正常返回结果。
