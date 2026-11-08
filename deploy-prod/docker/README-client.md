# 客户端部署（snetd）

客户端 A：Docker Compose（本目录 `compose.client.yaml`）。
客户端 B：手动 scp 二进制 + systemd 或 Docker。

客户端硬编码协调地址：`https://snet.uizhi.eu.org:8090`；不需要 `--server` 参数。

## 1. 宿主机前置（客户端 A）

- Linux 主机，root 或 sudo
- Docker + Docker Compose v2
- 内核 TUN：`ls -l /dev/net/tun` 必须存在
- 容器需要 `NET_ADMIN` 和 `/dev/net/tun`
- 公网/局域网 UDP 52100（若需直连）可入站

## 2. 构建并启动客户端 A

```bash
cd deploy-prod/docker
docker compose -f compose.client.yaml up -d --build
```

或先构建镜像：

```bash
docker build -f Dockerfile.client -t snetd:local ../..
docker compose -f compose.client.yaml up -d
```

配置目录 `/etc/snet` 挂载到容器；ctl-token 自动生成在 `/etc/snet/ctl-token`。

## 3. 首次绑定 / 建网

```bash
# Docker 客户端：snetctl 在容器内，使用 docker compose exec
docker compose -f compose.client.yaml exec snetd \
  snetctl -ctl http://127.0.0.1:19432 bind --code <设备授权码>

docker compose -f compose.client.yaml exec snetd \
  snetctl -ctl http://127.0.0.1:19432 create --name my-net

docker compose -f compose.client.yaml exec snetd \
  snetctl -ctl http://127.0.0.1:19432 status
```

裸机客户端则使用：

```bash
export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token
snetctl -ctl http://127.0.0.1:19432 bind --code <设备授权码>
```

若服务器未启用 `RequireDeviceAuth`，可跳过 `bind`，直接 create/join。

## 4. 网络模式选择

- **host network（推荐）**：TUN、直连、LAN/公网地址最接近裸机；ctl 绑定 `127.0.0.1:19432`，WG 端口 52100 直接在宿主。
- **bridge**：可行但需要额外端口映射、路由与 `NET_ADMIN`；容器 NAT 会改变源端口，直连候选可能单向，排障成本更高。
- 本 Compose 使用 `network_mode: host`，因此没有 `ports:`；ctl 仅本机，WG 52100/udp 由 snetd 直接监听。

## 5. 客户端 B 手动部署（scp + systemd）

构建：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o snetd ./cmd/client/snetd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o snetctl ./cmd/client/snetctl
scp snetd snetctl root@<client-b>:/usr/local/bin/
```

在客户端 B：

```bash
mkdir -p /etc/snet
/usr/local/bin/snetd \
  -config /etc/snet/config.json \
  -ctl 127.0.0.1:19432 \
  -device-id-file /etc/snet/device.id
```

systemd 示例：

```ini
[Unit]
Description=SNET daemon
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/snetd -config /etc/snet/config.json -ctl 127.0.0.1:19432 -device-id-file /etc/snet/device.id
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

绑定/建网：

```bash
export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token
snetctl -ctl http://127.0.0.1:19432 bind --code <设备授权码>
snetctl -ctl http://127.0.0.1:19432 status
```

客户端 B 也可用 Docker：需要完整源码/构建上下文，或直接使用预构建的 `snetd:local` 镜像；只复制 `compose.client.yaml` 和 `Dockerfile.client` 不足以完成 build。

## 6. 验证

```bash
curl -sS http://127.0.0.1:19432/ctl/status -H "X-Ctl-Token: $(cat /etc/snet/ctl-token)"
curl -sS http://127.0.0.1:19432/ctl/peers?nid=<nid> -H "X-Ctl-Token: $(cat /etc/snet/ctl-token)"
```

检查：

- `/ctl/status` 显示网络 active；
- `/ctl/peers` 能看到对端；
- 直连/relay 状态符合预期；
- 若 `RequireDeviceAuth=true`，先由管理员生成设备授权码。

## 7. 注意

- 不要把 `/etc/snet`（config、device.id、ctl-token）提交或公开。
- ctl 只绑定 `127.0.0.1:19432`，不要暴露到公网。
- 没有 `/dev/net/tun` 或 `NET_ADMIN` 时 snetd 可启动但隧道会持续 retry。
- 客户端 B 的 WG 端口不要与 relay 池 51820-52075 冲突；使用 52100+。
