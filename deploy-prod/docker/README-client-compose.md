# 客户端 Docker Compose 部署（A/B 通用）

> 客户端用 Docker Compose 手动部署；opencode 容器不访问宿主机 Docker daemon，本文件只提供产物与步骤。
> 客户端 A：opencode 宿主机（Docker + TUN）。客户端 B：另一台外网服务器。
> 镜像使用 `COPY` 预编译 `snetd`，不在客户端构建 Go。

## 1. 前置检查

在目标客户端执行：

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

- `x86_64` → `BIN_ARCH=amd64`；`aarch64` → `BIN_ARCH=arm64`
- `/dev/net/tun` 存在
- Docker + Compose 可用
- 19432/tcp、52100/udp 空闲
- 能访问服务端 `/healthz`

## 2. 目录准备

Compose 的 build context 是 `../..`，因此宿主机上需要保持：

```text
<parent>/
├── deploy-prod/docker/
│   ├── Dockerfile.client
│   ├── compose.client.yaml
│   └── README-client-compose.md
└── dist-deploy/
    └── linux-amd64/
        ├── snetd
        ├── server        # 可选，客户端不需要
        └── relay         # 可选，客户端不需要
```

把 `deploy-prod/docker/` 和 `dist-deploy/` 放到同一父目录。  
如果目录结构不同，修改 `compose.client.yaml` 的 `build.context` 指向包含 `dist-deploy/` 的目录。
客户端 B 还需要 `deploy-prod/manual-client/config.json.example`（或自行创建 config.json）。

## 3. 配置

```bash
install -d -m 0700 /etc/snet
cp deploy-prod/manual-client/config.json.example /etc/snet/config.json
chmod 0600 /etc/snet/config.json
```

`config.json.example` 是合法 JSON；`_comment` 会被 snetd 忽略。实际字段：

- `serverAddr`: `https://snet.uizhi.eu.org:8090`
- `wireguardPort`: 客户端 A 用 `52100`，客户端 B 用 `52101`
- `serverCaPath`: 空 = 系统信任（Let's Encrypt）
- `networks`: 首次为空
- `natMapping`: `true`

ctl 监听地址是 CLI `-ctl`，不是 config 字段；device-id 路径是 `-device-id-file`。首次启动会自动生成：

```text
/etc/snet/device.id
/etc/snet/ctl-token
```

不要提交/公开 `/etc/snet`。

## 4. 构建与启动

客户端 A（有构建环境）：

```bash
cd deploy-prod/docker
BIN_ARCH=amd64 docker compose -f compose.client.yaml up -d --build
```

`network_mode: host` 下：

- ctl 直接监听 `127.0.0.1:19432`
- WG 端口直接监听宿主 `52100/udp`（B 用 52101）
- 不设置 compose `ports:`，也不设置 memory limit（客户端内存通常充足）

### 客户端 B 无构建环境

在客户端 A 构建并导出：

```bash
docker save snet-client:latest | gzip > snet-client.tgz
scp snet-client.tgz root@<client-b>:/tmp/
```

客户端 B：

```bash
gunzip -c /tmp/snet-client.tgz | docker load
# 需要 deploy-prod/docker/、dist-deploy/（compose 仍引用 build context）
cd deploy-prod/docker
docker compose -f compose.client.yaml up -d
```

不要加 `--build`；使用已 load 的 `snet-client:latest`。

## 5. 验证

```bash
cd deploy-prod/docker
docker compose -f compose.client.yaml ps
docker logs snetd --tail 30

TOKEN=$(cat /etc/snet/ctl-token)
curl -s -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status | head -c 500
```

期望：

- 容器 running；
- ctl 返回 `mode=relay-v2` / 网络状态；
- 日志无 TUN 权限错误。

## 6. 建网 / 入网

先在服务端完成 admin bootstrap：

```text
https://snet.uizhi.eu.org:8090/admin
```

客户端 A 创建网络（Web UI 或宿主机 `snetctl`）：

```bash
# 镜像只包含 snetd；snetctl 用宿主二进制
install -m 0755 dist-deploy/snetctl-linux-amd64 /usr/local/sbin/snetctl
export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token
snetctl -ctl http://127.0.0.1:19432 create --name my-net
```

拿到 pairing code 后，客户端 B 加入：

```bash
install -m 0755 dist-deploy/snetctl-linux-amd64 /usr/local/sbin/snetctl
export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token
snetctl -ctl http://127.0.0.1:19432 join --nid <nid> --code <code>
```

若 `ApprovalRequired=true`，A 在 Web UI/`snetctl` 审批；随后两边：

```bash
curl -s -H "X-Ctl-Token: $(cat /etc/snet/ctl-token)" \
  http://127.0.0.1:19432/ctl/peers?nid=<nid>
```

检查 `peers` 与 `relayPath.peerDirect`。

## 7. 故障排查

| 现象 | 检查 |
|---|---|
| TUN 创建失败 | `--cap-add NET_ADMIN` + `devices: /dev/net/tun`；宿主内核 TUN |
| 端口冲突 | host network 下 19432/52100 必须空闲；B 改 52101 |
| ctl 401 | `SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token`；token 是否被删/重建 |
| 服务端不可达 | `curl -fsS https://snet.uizhi.eu.org:8090/healthz` |
| 容器启动即退出 | `docker logs snetd --tail 50`；检查 config JSON 是否合法 |
| 直连不通 | 检查公网 UDP 52100/52101、NAT 映射、`relayPath.peerDirect` |

## 8. A/B 差异

| 项 | 客户端 A（宿主机） | 客户端 B（远程） |
|---|---|---|
| 文件 | 已有 `deploy-prod/` + `dist-deploy/` | scp 两个目录，或 scp 镜像 tar |
| 构建 | `docker compose ... up -d --build` | `docker load` 后 `up -d`，不 `--build` |
| WG 端口 | 52100 | 52101 |
| ctl | 127.0.0.1:19432 | 127.0.0.1:19432（各自本机） |

## 9. 说明

- `Dockerfile.client` 只 COPY `snetd`，不包含 `snetctl`；`snetctl` 用 `dist-deploy` 的宿主二进制或 Web UI。
- 不使用 Docker MCP 自动部署；所有命令由用户在宿主机执行。
- 不提交 `/etc/snet/ctl-token`、`device.id`、config 或镜像 tar。
