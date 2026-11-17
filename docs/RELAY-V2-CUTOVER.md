# Relay v2 Flag-Day 切换手册

> 状态：预检完成，**尚未执行新的切换操作**。
>
> 目标：把所有客户端稳定切到 Relay v2，并在出现大面积问题时能快速回退。

## 0. 预检结论（2026-11-16）

当前生产环境已经存在一套正在运行的 Relay v2：

- `snet-server` 已向客户端广告 `snet.uizhi.eu.org:8092`。
- `snet-relay` 正在 `8092/udp` 监听，`/healthz` 返回 `status=ok`。
- NAS 上的 `HomeNET` 当前 `relayPath.mode=relay-v2`。
- VPS `snet-server` / `snet-relay` / Hub / NAS/sdserver 均为 `v1.1.13`。

因此本手册的“切换”实际包含两件事：

1. 把广告地址从域名切换到固定 IP `66.187.6.46:8092`。
2. 可选地轮换 Relay v2 ticket secret。

不要在未确认所有客户端版本前执行第 2 步。

## 1. 前置条件

所有可能访问同一网络的设备必须升级到 **v1.1.13 或更高版本**：

- VPS `snet-server`、`snet-relay`
- 官方 Hub
- NAS
- sdserver
- macOS 客户端
- Windows 客户端
- Android 客户端（`v1.1.13` release APK 或更高）

检查示例：

```bash
# VPS 服务端 / Relay
docker exec snet-server /usr/local/bin/server -version
docker exec snet-relay /usr/local/bin/relay -version

# NAS / sdserver
docker exec snetd /usr/local/bin/snetd -version

# Hub
docker exec snet-hub /usr/local/bin/snetd -version
```

Mac / Windows / Android 在客户端“设置 → 关于”中确认版本不低于 `v1.1.13`。

未升级的旧客户端可能与新 Relay v2 协议不兼容。**只要还有旧客户端在线，不得执行正式切换。**

## 2. 当前 VPS 文件与暂存内容

目录：`/opt/snet-deploy`

本次预检已准备但未激活：

- `.env` 中新增单数变量 `SNET_RELAY_V2_ENDPOINT=66.187.6.46:8092`（compose 当前不引用它）。
- 候选 secret 曾写入 `/opt/snet-deploy/.relay-v2-secret.candidate`，权限 `0600`。
- 2026-11-16 已完成 Secret 轮换：候选 secret 成为新的 `SNET_RELAY_SECRET`，旧 secret 暂存为 `SNET_RELAY_SECRET_OLD`。
- 当前 compose 已同时向 relay/server 注入 `SNET_RELAY_SECRET` 与 `SNET_RELAY_SECRET_OLD`。
- 候选 secret 文件在成功写入 `.env` 后已删除。
- 当前广告 endpoint 仍为 `snet.uizhi.eu.org:8092`；固定 IP endpoint 尚未切换。

切换前先备份：

```bash
cd /opt/snet-deploy
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
cp -a compose.yaml "compose.yaml.bak-relayv2-$STAMP"
cp -a .env ".env.bak-relayv2-$STAMP"
```

同时备份 Relay 健康基线：

```bash
docker exec snet-relay wget -qO- http://127.0.0.1:8094/healthz
ss -lunp | grep ':8092' || true
docker logs --tail 100 snet-server 2>&1 | grep -i 'relay v2'
```

## 3. 服务端准备检查

确认 Relay 独立进程正常：

```bash
docker exec snet-relay wget -qO- http://127.0.0.1:8094/healthz
```

期望：

```json
{"status":"ok","sessions":N,"packetsIn":N,"packetsOut":N}
```

确认 UDP 端口：

```bash
ss -lunp | grep ':8092'
```

期望看到 `0.0.0.0:8092` 或 `[::]:8092` 的 UDP 监听。

## 4. 正式切换步骤

> 以下步骤只在确认所有设备均为 `v1.1.13+` 后执行。

### 4.1 选择 secret 策略

**推荐：使用双 secret 滚动轮换。** 直接把新 secret 写入 relay 会让旧 ticket 全部失效；实测会造成 60 秒以上无 session。正确做法是让 relay/server 同时接受新旧 secret。

- 新 secret 作为 `SNET_RELAY_SECRET`（用于签发新 ticket）。
- 旧 secret 作为 `SNET_RELAY_SECRET_OLD`（只用于验证旧 ticket）。
- `snet-relay` 与 `snet-server` 必须同时拿到两个值。
- 当前 compose 已在 `relay` 和 `server` 两个 service 中注入：

```yaml
      SNET_RELAY_SECRET_OLD: ${SNET_RELAY_SECRET_OLD:-}
```

### 4.2 修改 `.env`

把当前生效的复数变量改为固定 IP：

```dotenv
SNET_RELAY_V2_ENDPOINTS=66.187.6.46:8092
```

如果轮换 secret：

```dotenv
SNET_RELAY_SECRET=<new-secret>
SNET_RELAY_SECRET_OLD=<old-secret>
```

不要使用 `echo` 打印 secret。可以从候选文件读取到临时变量后写入。

轮换完成后保留旧 secret 至少 `TicketTTL`（1 小时），让旧 ticket 自然过期。之后执行清理：

1. 从 `.env` 删除 `SNET_RELAY_SECRET_OLD`。
2. 从 `compose.yaml` 两个 service 的 `environment` 中删除 `SNET_RELAY_SECRET_OLD` 行。
3. 依次重启 `snet-relay` 和 `snet-server`。

### 4.3 重启 Relay 与 Server

轮换 secret 时两者必须一起更新：

```bash
cd /opt/snet-deploy
docker compose up -d relay server
```

只切换 endpoint、不轮换 secret 时，至少重启 server；Relay 可以保持运行：

```bash
docker compose up -d server
```

### 4.4 验证服务端

```bash
docker exec snet-relay wget -qO- http://127.0.0.1:8094/healthz
docker logs --tail 80 snet-server 2>&1 | grep 'relay v2'
```

期望日志中出现：

```text
relay v2: tickets for 66.187.6.46:8092
```

健康检查：

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
```

## 5. 客户端验证

### 5.1 NAS / sdserver relay 模式

```bash
TOKEN=$(cat /opt/stacks/snet/data/ctl-token)
curl -s -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); print(json.dumps([{"name":n.get("name"),"mode":(n.get("relayPath") or {}).get("mode")} for n in d.get("networks",[])],ensure_ascii=False,indent=2))'
```

期望每个活动网络：

```json
{"name":"HomeNET","mode":"relay-v2"}
```

### 5.2 `/peers` 中的 RelayV2

```bash
TOKEN=$(cat /opt/stacks/snet/data/ctl-token)
NID=$(curl -s -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status \
  | python3 -c 'import sys,json; print((json.load(sys.stdin).get("networks") or [{}])[0].get("networkId",""))')
curl -s -H "X-Ctl-Token: $TOKEN" "http://127.0.0.1:19432/ctl/peers?nid=$NID" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get("relayV2"),ensure_ascii=False,indent=2))'
```

期望：

```json
{"enabled":true,"endpoint":"66.187.6.46:8092","endpoints":["66.187.6.46:8092"]}
```

### 5.3 P2P 直连

在 Relay 建立后观察最多 10 秒：

```bash
curl -s -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); [print(n.get("name"), json.dumps((n.get("relayPath") or {}).get("peerDirect",{}),ensure_ascii=False)) for n in d.get("networks",[])]'
```

`peerDirect` 中应逐步出现 `"path":"direct"`。若长期只有 `relay`，检查：

- 双方公网 UDP 端口
- NAT 类型（symmetric NAT 可能只能走 relay）
- IPv6 连通性
- 防火墙是否放行 WireGuard 和 8092/udp

## 6. 回滚方案

### 6.1 只回滚 endpoint

```bash
cd /opt/snet-deploy
sed -i 's|^SNET_RELAY_V2_ENDPOINTS=.*|SNET_RELAY_V2_ENDPOINTS=snet.uizhi.eu.org:8092|' .env
docker compose up -d server
```

### 6.2 回滚 secret

```bash
cd /opt/snet-deploy
cp -a .env.bak-relayv2-<STAMP> .env
docker compose up -d relay server
```

### 6.3 完全回退到 Legacy 中继

仅从 `.env` 删除变量不够：当前 compose 对 `SNET_RELAY_V2_ENDPOINTS` 使用了默认值 `snet.uizhi.eu.org:8092`，空值仍会回落到默认。

必须修改 `compose.yaml`，从 `server` 的 `environment` 中删除或注释：

```yaml
      SNET_RELAY_V2_ENDPOINTS: ${SNET_RELAY_V2_ENDPOINTS:-snet.uizhi.eu.org:8092}
```

然后：

```bash
docker compose up -d server
```

验证 `/peers` 不再返回 `relayV2`，客户端 `relayPath.mode` 回到 legacy 模式。

## 7. 切换后观察清单

- [ ] `snet-relay` `/healthz` 为 ok，8092/udp 持续监听
- [ ] `snet-server` 日志显示新的 Relay v2 endpoint
- [ ] NAS / sdserver `relayPath.mode=relay-v2`
- [ ] `/peers` 中 `relayV2.enabled=true` 且 endpoint 正确
- [ ] 至少一对设备在 10 秒内建立 direct 路径
- [ ] Mac / Windows / Android 客户端网络详情正常
- [ ] 无大面积断网、无持续重连

## 8. 禁止事项

- 不要在仍有 `v1.1.9` 或更旧客户端时切换。
- 不要在未备份 `.env` 和 `compose.yaml` 时切换。
- 不要只重启 server 而不同时更新 relay 的 secret。
- 不要把 secret 写入 Git、聊天记录或普通日志。
