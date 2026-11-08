# 客户端 Docker Compose 部署（A/B 通用）

> 客户端 A/B 都用预构建镜像 `snet-client:latest`；本目录不含 build，不需要 Go/Docker build。
> 镜像 tar 由 VPS 构建：`/tmp/snet-client-linux-amd64.tgz`。
> 部署由用户在宿主机手动执行。

## 1. 前置检查

```bash
uname -m
ls -l /dev/net/tun
docker --version
docker compose version
ss -lntup | grep -E ':(19432|52100)' || true
curl -s https://ifconfig.me; echo
curl -fsS https://snet.uizhi.eu.org:8090/healthz
```

期望：

- `x86_64` → amd64 镜像；`aarch64` → 需 arm64 镜像（当前 tar 为 amd64）
- `/dev/net/tun` 存在
- Docker + Compose 可用
- 19432/tcp、52100/udp 空闲
- 服务端 `/healthz` 可访问

## 2. 目录准备

标准 Linux（客户端 A）：

```text
/opt/snet-deploy/deploy-prod/docker-client/
├── compose.yaml
├── config.json.example
└── README.md
```

群晖 NAS（客户端 B 参考）：

```text
/volume1/docker/snet/deploy-prod/docker-client/
├── compose.yaml
├── config.json.example
└── README.md
```

本目录只依赖 `compose.yaml` + `config.json.example`；不再需要 `dist-deploy/` 或 `Dockerfile.client` 来启动。

## 3. 加载镜像

从 VPS 获取镜像 tar（示例）：

```bash
# VPS 上已有 /tmp/snet-client-linux-amd64.tgz
scp root@66.187.6.46:/tmp/snet-client-linux-amd64.tgz .
# 或使用临时 HTTP（如仍开启）
# curl -fLO http://66.187.6.46:18080/snet-client-linux-amd64.tgz
```

加载：

```bash
docker load -i snet-client-linux-amd64.tgz
docker images | grep snet-client
```

预期镜像：`snet-client:latest`。
当前 tar 的 sha256：

```text
40524219397b5203779f1eceaad291749157fb0772605ade1e89d4febe45c9cb
```

## 4. 配置

标准 Linux（客户端 A）：

```bash
install -d -m 0700 /etc/snet
cp config.json.example /etc/snet/config.json
chmod 0600 /etc/snet/config.json
```

群晖 NAS（客户端 B）：

```bash
install -d -m 0700 /volume1/docker/snet/etc
cp config.json.example /volume1/docker/snet/etc/config.json
chmod 0600 /volume1/docker/snet/etc/config.json
```

字段：

- `serverAddr`: `https://snet.uizhi.eu.org:8090`
- `wireguardPort`: A 用 `52100`，B 用 `52101`
- `serverCaPath`: 空 = 系统信任（Let's Encrypt）
- `networks`: 首次为空
- `natMapping`: `true`

ctl 监听地址是 CLI `-ctl`；device-id 路径是 `-device-id-file`。首次启动自动生成：

```text
device.id
ctl-token
```

### 群晖 /etc/snet 重定向注意

Container Manager 可能把 `/etc/snet` 挂载重定向到：

```text
/volume1/@appdata/ContainerManager/all_shares/docker/snet/etc
```

检查实际挂载：

```bash
docker inspect snetd --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}'
```

ctl-token 在**实际宿主机挂载源目录**下，例如：

```text
/volume1/@appdata/ContainerManager/all_shares/docker/snet/etc/ctl-token
```

标准 Linux 设备 A 一般在 `/etc/snet/ctl-token`。

## 5. 启动

```bash
cd /volume1/docker/snet/deploy-prod/docker-client   # NAS
# 或 cd /opt/snet-deploy/deploy-prod/docker-client  # 标准 Linux

docker compose up -d
```

不再使用 `-f compose.client.yaml`，也不再 `--build`。
`network_mode: host` 下 ctl 直接监听 `127.0.0.1:19432`，WG 直接监听宿主 `52100/udp`（B 为 52101）。

## 6. 验证

```bash
docker compose ps
docker logs snetd --tail 30

# 标准 Linux
TOKEN=$(cat /etc/snet/ctl-token)
# 群晖使用实际挂载源路径，例如：
# TOKEN=$(cat /volume1/@appdata/ContainerManager/all_shares/docker/snet/etc/ctl-token)

curl -s -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status | head -c 500
```

## 7. 建网 / 入网

先在服务端完成 admin bootstrap：

```text
https://snet.uizhi.eu.org:8090/admin
```

`compose.yaml` 只包含 `snetd`，不包含 `snetctl`。`snetctl` 用宿主机二进制：

```bash
# 从 VPS 下载或使用已有 dist-deploy
scp root@66.187.6.46:/tmp/snetctl-linux-amd64 /usr/local/sbin/snetctl
chmod 0755 /usr/local/sbin/snetctl

export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token   # 群晖改成实际 ctl-token 路径

# A 创建网络
snetctl -ctl http://127.0.0.1:19432 create --name my-net

# B 加入（拿到 A 的 nid + code）
snetctl -ctl http://127.0.0.1:19432 join --nid <nid> --code <code>
```

若 `ApprovalRequired=true`，A 在 Web UI/`snetctl` 审批。随后检查：

```bash
curl -s -H "X-Ctl-Token: $(cat /etc/snet/ctl-token)" \
  'http://127.0.0.1:19432/ctl/peers?nid=<nid>'
```

## 8. 故障排查

| 现象 | 检查 |
|---|---|
| TUN 创建失败 | `cap_add: NET_ADMIN` + `devices: /dev/net/tun`；宿主内核 TUN |
| 端口冲突 | host network 下 19432/52100 空闲；B 改 52101 |
| ctl 401 | `SNET_CTL_TOKEN_FILE` 是否指向实际 ctl-token；群晖路径可能被重定向 |
| 服务端不可达 | `curl -fsS https://snet.uizhi.eu.org:8090/healthz` |
| 容器启动即退出 | `docker logs snetd --tail 50`；检查 config JSON |
| 直连不通 | 公网 UDP 52100/52101、NAT 映射、`relayPath.peerDirect` |

## 9. A/B 差异

| 项 | 客户端 A（标准 Linux） | 客户端 B（NAS/远程） |
|---|---|---|
| 部署目录 | `/opt/snet-deploy/deploy-prod/docker-client` | `/volume1/docker/snet/deploy-prod/docker-client` |
| 配置 | `/etc/snet` | `/volume1/docker/snet/etc` 或 ContainerManager 重定向路径 |
| 镜像 | `docker load` 同一 tar | `docker load` 同一 tar |
| WG 端口 | 52100 | 52101 |
| ctl | 127.0.0.1:19432 | 127.0.0.1:19432（各自本机） |

## 10. 说明

- 本目录不包含 build 段；镜像 tar 由 VPS 构建。
- `Dockerfile.client` 仅作为可选重建参考保留，不是部署必需。
- 不使用 Docker MCP 自动部署。
- 不提交 `ctl-token`、`device.id`、config 或镜像 tar。
