# 客户端 Docker Compose 部署（A/B 通用）

> 客户端 A/B 都用预构建镜像；本目录不含 build，不需要 Go/Docker build。
> 推荐从 VPS 的 basic-auth registry 拉取：`66.187.6.46:5000/snet-client:latest`。
> 离线 tar 方案保留：`/tmp/snet-client-linux-amd64.tgz`。
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
├── Dockerfile.web
├── nginx.conf
├── docker-entrypoint.sh
└── README.md
```

群晖 NAS（客户端 B 参考）：

```text
/volume1/docker/snet/deploy-prod/docker-client/
├── compose.yaml
├── config.json.example
├── Dockerfile.web
├── nginx.conf
├── docker-entrypoint.sh
└── README.md
```

本目录只依赖 `compose.yaml` + `config.json.example`；不再需要 `dist-deploy/` 或 `Dockerfile.client` 来启动。

## 3. 获取镜像

### 方案 A：VPS Registry（推荐长期）

Registry：`66.187.6.46:5000`，HTTP + basic auth。

先在客户端配置 insecure registry：

```bash
sudo tee /etc/docker/daemon.json >/dev/null <<'JSON'
{
  "insecure-registries": ["66.187.6.46:5000"]
}
JSON
sudo systemctl restart docker
```

注意：

- 群晖重启 Docker 会中断 penpot/dpanel 等其他容器，建议维护窗口。
- 标准 Linux 设备 A 同样需要 `insecure-registries`。
- 若已有 `/etc/docker/daemon.json`，合并 `insecure-registries`，不要覆盖其他字段。

登录（密码由管理员提供，不写入仓库/脚本）：

```bash
docker login 66.187.6.46:5000 -u snet
```

拉取：

```bash
cd /volume1/docker/snet/deploy-prod/docker-client   # NAS
# 或 cd /opt/snet-deploy/deploy-prod/docker-client  # 标准 Linux
docker compose pull snetd
docker compose up -d
```

更新镜像：

```bash
docker compose pull snetd && docker compose up -d
```

### 方案 B：离线 tar（备选）

适合不想修改 Docker 配置的场景。

```bash
# VPS 上已有
scp root@66.187.6.46:/tmp/snet-client-linux-amd64.tgz .
docker load -i snet-client-linux-amd64.tgz
cd /volume1/docker/snet/deploy-prod/docker-client
docker compose up -d
```

离线 tar sha256：

```text
40524219397b5203779f1eceaad291749157fb0772605ade1e89d4febe45c9cb
```

> 注意：当前 `compose.yaml` 的 image 是 registry 地址；使用离线 tar 时，可临时把 image 改回 `snet-client:latest`，或在 A 上 `docker tag snet-client:latest 66.187.6.46:5000/snet-client:latest` 后再 compose up。

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
`network_mode: host` 下 snetd 监听 `0.0.0.0:19432`（供 bridge 的 Web 容器经 host.docker.internal 访问），WG 直接监听宿主 `52100/udp`（B 为 52101）。请用防火墙限制 19432/tcp，不要暴露公网。

### Web GUI

Web 容器 `snet-web` 用 nginx 托管 `shared/web`，并反向代理 snetd `/ctl`。`shared/web` 没有 `package.json`/构建脚本；实际使用 `deploy/docker/web-build/web.ts` 适配器 + esbuild。

在客户端 A 或 VPS 构建：

```bash
cd deploy-prod/docker-client
docker build -f Dockerfile.web -t snet-web:latest ../..
```

NAS 无构建环境时，可在 VPS 构建后推送到 registry：

```bash
docker tag snet-web:latest 66.187.6.46:5000/snet-web:latest
docker push 66.187.6.46:5000/snet-web:latest
# 客户端 docker pull 后，如需 registry 拉取，把 compose 的 web.image 改为
# 66.187.6.46:5000/snet-web:latest
```

启动后浏览器访问：

```text
http://<NAS_IP>:8080
```

首次使用：Web UI 会要求设置/输入 **Web 管理密码**；不会要求输入 ctl-token。nginx 的 entrypoint 会从 `/etc/snet/ctl-token` 注入 `X-Ctl-Token`。如果页面报 401/ctl 错误，检查：

```text
/volume1/@appdata/ContainerManager/all_shares/docker/snet/etc/ctl-token
# 或标准 Linux
/etc/snet/ctl-token
```

8080 被占用时把 compose 的 `ports` 改为 `"8081:80"`。

> 说明：任务原始 nginx 配置只透传浏览器 `X-Ctl-Token`；当前 shared/web 不会发送该 header，因此这里用 `docker-entrypoint.sh` 从 ctl-token 文件注入，并设置 `X-Snet-Web: 1`。

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

`compose.yaml` 包含 `snetd` + `snet-web`；两个镜像都不包含 `snetctl`。`snetctl` 用宿主机二进制：

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
| 镜像 | registry `docker compose pull snetd` 或离线 tar | registry `docker compose pull snetd` 或离线 tar |
| WG 端口 | 52100 | 52101 |
| ctl | 127.0.0.1:19432 | 127.0.0.1:19432（各自本机） |

## 10. 安全说明

- Registry：`66.187.6.46:5000`，basic auth 保护；不是匿名 registry。
- Registry 密码在 VPS：`/opt/snet-registry/auth/password.txt`，仅管理员可见；不要把密码写入仓库、脚本或聊天。
- 公网访问 5000/tcp 需要 Hostodo 防火墙放行。
- 客户端必须配置 `insecure-registries`，因为 registry 当前是 HTTP。
- `docker login` 凭据保存在客户端 Docker 配置中；共享主机注意权限。
- 为让 bridge 的 Web 容器访问 host-network 的 snetd，`-ctl` 绑定 `0.0.0.0:19432`；请用防火墙限制 19432/tcp，仅允许本机/Docker bridge，且 ctl-token 仍必须保密。
- Web 容器挂载 `/etc/snet` 只读，仅用于读取 ctl-token；不要挂载可写目录。

## 11. 说明

- 本目录不包含 build 段；镜像由 VPS 构建并推送到 registry，或使用离线 tar。
- `Dockerfile.client` 仅作为可选重建参考保留，不是部署必需。
- 不使用 Docker MCP 自动部署。
- 不提交 `ctl-token`、`device.id`、config、registry 密码或镜像 tar。
