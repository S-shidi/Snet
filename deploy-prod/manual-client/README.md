# SNET 客户端手动部署（A/B 通用）

> 客户端 A、B 均手动部署；不再使用 Docker MCP 自动部署。
> 协调服务器固定为 `https://snet.uizhi.eu.org:8090`；`snetd`/`snetctl` 没有 `--server` 参数。
> 本目录只提供产物与步骤；实际部署由用户执行。

## 0. 产物

`dist-deploy/` 内（不提交，已 gitignore）：

```text
snetd-linux-amd64
snetd-linux-arm64
snetctl-linux-amd64
snetctl-linux-arm64
SHA256SUMS
```

本目录的 `SHA256SUMS` 记录同一组哈希；部署前校验：

```bash
cd dist-deploy
sha256sum -c ../deploy-prod/manual-client/SHA256SUMS
```

按 `uname -m` 选择：

- `x86_64` → `linux-amd64`
- `aarch64`/`arm64` → `linux-arm64`

## 1. 前置检查

把 `check-prereq.sh` 拷到目标机执行（只读，不改系统）：

```bash
bash check-prereq.sh
```

检查项：

- 架构是否 amd64/arm64
- `/dev/net/tun` 是否存在
- 当前进程是否有 `CAP_NET_ADMIN`
- 19432/tcp、52100/udp 是否空闲
- 公网出口 IP

全部 PASS 再继续。没有 TUN 或 `CAP_NET_ADMIN` 时，snetd 可启动但隧道会持续 retry。

## 2. 安装二进制

在本地/构建机：

```bash
scp dist-deploy/snetd-linux-amd64 root@<client>:/usr/local/snet/bin/snetd
scp dist-deploy/snetctl-linux-amd64 root@<client>:/usr/local/snet/bin/snetctl
```

目标机：

```bash
install -d -m 0755 /usr/local/snet/bin /etc/snet
chmod 0755 /usr/local/snet/bin/snetd /usr/local/snet/bin/snetctl
```

arm64 主机换成 `linux-arm64`。

## 3. 配置

复制模板：

```bash
cp config.json.example /etc/snet/config.json
chmod 0600 /etc/snet/config.json
```

`config.json.example` 是合法 JSON；`_comment` 字段会被 snetd 忽略。实际字段：

- `serverAddr`：固定 `https://snet.uizhi.eu.org:8090`
- `wireguardPort`：本机 WireGuard 端口，默认示例 `52100`
- `serverCaPath`：空 = 系统信任（Let's Encrypt）；自签时才填 CA PEM 路径
- `networks`：首次为空，由 create/join 写入
- `keyRotationDays`：可选，0 关闭
- `natMapping`：`true` 开启 UPnP/NAT-PMP/PCP

注意：

- **ctl 监听地址不在 config**，是 `snetd -ctl` 参数。
- **日志级别当前没有配置字段**；日志走 systemd/journald 默认输出。
- **device-id 文件路径不在 config**，是 `snetd -device-id-file` 参数。
- `privateKey`、`deviceId` 首次启动自动生成；不要手写或提交。

## 4. systemd

安装 unit：

```bash
install -m 0644 snetd.service /etc/systemd/system/snetd.service
systemctl daemon-reload
systemctl enable --now snetd
```

unit 关键点：

- `ExecStart=... -config /etc/snet/config.json -ctl 127.0.0.1:19432 -device-id-file /etc/snet/device.id`
- `AmbientCapabilities=CAP_NET_ADMIN`
- `Restart=on-failure`
- ctl-token 由 snetd 首次启动生成在 `/etc/snet/ctl-token`，供本机 Web/ctl 客户端读取

查看日志：

```bash
journalctl -u snetd -f
```

## 5. 首次连接 / 建网

ctl-token 位置：`/etc/snet/ctl-token`（snetd 生成，0644；只在本机使用）。

```bash
export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token
CTL=http://127.0.0.1:19432

# 若服务器启用了 RequireDeviceAuth：
snetctl -ctl "$CTL" bind --code <设备授权码>

# 建网（owner）
snetctl -ctl "$CTL" create --name my-net

# 或加入已有网络（二选一）
snetctl -ctl "$CTL" join --link '<snet://join?...>'
# 或
snetctl -ctl "$CTL" join --nid <nid> --code <invite-code>

# 查看状态
snetctl -ctl "$CTL" status
snetctl -ctl "$CTL" peers <nid>
```

若服务器未启用 `RequireDeviceAuth`，可跳过 `bind`。

## 6. 验证

```bash
TOKEN=$(cat /etc/snet/ctl-token)
curl -sS -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status
curl -sS -H "X-Ctl-Token: $TOKEN" 'http://127.0.0.1:19432/ctl/peers?nid=<nid>'
```

检查：

- `/ctl/status` 显示网络 active；
- `/ctl/peers` 能看到对端；
- `relayPath.peerDirect` 显示 direct/relay 状态；
- 从另一节点 ping overlay IP / 访问容器内服务。

## 7. 故障排查

| 现象 | 检查 |
|---|---|
| `create tun ... /dev/net/tun does not exist` | 宿主没有 TUN；需要 `--device /dev/net/tun` 或内核 TUN |
| `operation not permitted` / TUN 失败 | 缺少 `CAP_NET_ADMIN`；检查 systemd `AmbientCapabilities` |
| 端口冲突 | `ss -lntup \| grep -E ':(19432\|52100)'` |
| server 不可达 | `curl https://snet.uizhi.eu.org:8090/healthz`；检查 DNS/防火墙 |
| ctl 401 | `SNET_CTL_TOKEN_FILE` 是否指向 `/etc/snet/ctl-token`；token 是否被删除/重建 |
| 网络一直 retry | `journalctl -u snetd`；检查 TUN/权限/server/授权码 |
| 设备未授权 | 管理员生成授权码后执行 `snetctl bind --code ...` |
| 直连不通但 relay 通 | 检查公网 UDP 52100、NAT 映射、`/ctl/status` 的 direct/relay 状态 |

## 8. 客户端 B

客户端 B 使用同一套二进制和步骤：

1. 按 `uname -m` scp 对应 `snetd`/`snetctl`；
2. 复制 `config.json.example` 到 `/etc/snet/config.json`，改 `wireguardPort`（如 52101）；
3. 安装同一 `snetd.service`；
4. 启动后用 `snetctl join --link/--nid --code` 加入 A 创建的网络；
5. 验证 overlay ping/HTTP。

客户端 B 不需要 Docker；手动二进制 + systemd 即可。
