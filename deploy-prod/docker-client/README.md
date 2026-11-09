# 客户端部署（snetd + Web GUI 单镜像）

> 一个镜像、一个容器：snetd + nginx + shared/web，由 supervisord 管理。
> `network_mode: host`：nginx 直接代理 `127.0.0.1:19432`，不需要 host.docker.internal。
> 部署由用户在宿主机手动执行。

## 1. 前置检查

```bash
uname -m
ls -l /dev/net/tun
docker --version
docker compose version
ss -lntup | grep -E ':(8081|19432|52100)' || true
curl -s https://ifconfig.me; echo
curl -fsS https://snet.uizhi.eu.org:8090/healthz
```

期望：

- `x86_64` → amd64 镜像；`aarch64` → 需 arm64 镜像
- `/dev/net/tun` 存在
- Docker + Compose 可用
- 8081、19432、52100 空闲（B 的 WG 用 52101）
- 服务端 healthz 可访问

## 2. 目录

```text
deploy-prod/docker-client/
├── compose.yaml
├── Dockerfile.client
├── nginx.conf
├── nginx-entrypoint.sh
├── supervisord.conf
├── config.json.example
└── README.md
```

构建 context 是 `../..`（仓库根），需要以下路径可访问：

```text
shared/web/
deploy/docker/web-build/
deploy-prod/docker-client/nginx.conf
deploy-prod/docker-client/nginx-entrypoint.sh
deploy-prod/docker-client/supervisord.conf
dist-deploy/linux-amd64/snetd
```

## 3. 构建合并镜像

在客户端 A 或 VPS（有完整构建 context）执行：

```bash
cd deploy-prod/docker-client
docker build -f Dockerfile.client -t 66.187.6.46:5000/snet-client:latest ../..
```

构建需要：

```text
shared/web/
deploy/docker/web-build/
deploy-prod/docker-client/{nginx.conf,nginx-entrypoint.sh,supervisord.conf}
dist-deploy/linux-amd64/snetd
```

VPS registry 当前可能还是旧的 snetd-only 镜像；必须用本命令重建并重新推送。VPS 上推送时使用 loopback 别名（VPS Docker 只信任 loopback HTTP）：

```bash
docker tag 66.187.6.46:5000/snet-client:latest 127.0.0.1:5000/snet-client:latest
docker push 127.0.0.1:5000/snet-client:latest
```

## 4. 镜像获取

### 方案 A：VPS Registry（推荐）

Registry：`66.187.6.46:5000`，HTTP + basic auth。

客户端配置：

```bash
sudo tee /etc/docker/daemon.json >/dev/null <<'JSON'
{
  "insecure-registries": ["66.187.6.46:5000"]
}
JSON
sudo systemctl restart docker
```

群晖重启 Docker 会中断 penpot/dpanel，建议维护窗口。

登录：

```bash
docker login 66.187.6.46:5000 -u snet
```

拉取并启动：

```bash
cd /volume1/docker/snet   # NAS
# 或 cd /opt/stacks/snet  # 标准 Linux
docker compose pull
docker compose up -d
```

更新：

```bash
docker compose pull && docker compose up -d
```

### 方案 B：离线 tar

```bash
scp root@66.187.6.46:/tmp/snet-client-linux-amd64.tgz .
docker load -i snet-client-linux-amd64.tgz
docker tag snet-client:latest 66.187.6.46:5000/snet-client:latest
cd /volume1/docker/snet
docker compose up -d
```

> 注意：旧 `/tmp/snet-client-linux-amd64.tgz` 是 snetd-only 镜像，不含 nginx/Web GUI；必须重新构建合并镜像后再 `docker save` 新 tar。

## 5. 配置

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
- `networks`: 首次为空
- `natMapping`: `true`

首次启动自动生成：

```text
device.id
ctl-token
```

群晖 Container Manager 可能把 `/etc/snet` 重定向到：

```text
/volume1/@appdata/ContainerManager/all_shares/docker/snet/etc
```

检查实际挂载：

```bash
docker inspect snetd --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}'
```

ctl-token 在实际宿主机挂载源目录下。

## 6. 启动

```bash
cd /volume1/docker/snet
docker compose up -d
```

- `network_mode: host`：nginx 监听宿主机 8081，snetd 监听 127.0.0.1:19432 和 WG 52100/52101。
- 不需要 `ports:`；不需要单独的 `web` service。
- supervisord 启动 snetd + nginx；nginx 会等待 `/etc/snet/ctl-token` 后生成配置。

## 7. Web GUI

浏览器访问：

```text
http://<NAS_IP>:8081
```

首次使用：设置/输入 **Web 管理密码**。nginx 自动从 `/etc/snet/ctl-token` 注入 `X-Ctl-Token` 并设置 `X-Snet-Web: 1`；不需要在页面输入 ctl-token。

排障时 ctl-token 路径：

```text
/volume1/@appdata/ContainerManager/all_shares/docker/snet/etc/ctl-token
# 或标准 Linux
/etc/snet/ctl-token
```

8081 被占用时：

- 改 `nginx.conf` 的 `listen 8081;` 为 `8082`，重建镜像；或
- 改用 bridge + `ports: "8082:80"`，但需把 snetd `-ctl` 改为 `0.0.0.0:19432` 并用防火墙限制 19432；host network 更简单。

## 8. 验证

```bash
docker compose ps
docker logs snetd --tail 50

TOKEN=$(cat /etc/snet/ctl-token)
curl -s -H "X-Ctl-Token: $TOKEN" http://127.0.0.1:19432/ctl/status | head -c 500

curl -sI http://127.0.0.1:8081/ | head
```

期望：

- 容器 running；
- 8081 返回 HTML；
- ctl 返回 `mode=relay-v2` / 网络状态；
- 日志无 TUN/nginx/token 错误。

## 9. 建网 / 入网

先在服务端完成 admin bootstrap：

```text
https://snet.uizhi.eu.org:8090/admin
```

镜像不包含 `snetctl`；用宿主机二进制：

```bash
scp root@66.187.6.46:/tmp/snetctl-linux-amd64 /usr/local/sbin/snetctl
chmod 0755 /usr/local/sbin/snetctl

export SNET_CTL_TOKEN_FILE=/etc/snet/ctl-token   # 群晖改成实际 ctl-token 路径

# A 创建网络
snetctl -ctl http://127.0.0.1:19432 create --name my-net

# B 加入
snetctl -ctl http://127.0.0.1:19432 join --nid <nid> --code <code>
```

若 `ApprovalRequired=true`，A 在 Web UI/`snetctl` 审批。

## 10. 故障排查

| 现象 | 检查 |
|---|---|
| TUN 创建失败 | `cap_add: NET_ADMIN` + `devices: /dev/net/tun`；宿主内核 TUN |
| 8081 被占 | `ss -lntp \| grep :8081`；改 nginx listen 并重建，或维护窗口 |
| ctl 401 | `/etc/snet/ctl-token` 是否存在；群晖实际挂载路径 |
| nginx 退出 | `docker logs snetd`；是否缺少 ctl-token 或 nginx 配置错误 |
| 服务端不可达 | `curl -fsS https://snet.uizhi.eu.org:8090/healthz` |
| 直连不通 | 公网 UDP 52100/52101、NAT 映射、`relayPath.peerDirect` |

## 11. A/B 差异

| 项 | 客户端 A（标准 Linux） | 客户端 B（NAS/远程） |
|---|---|---|
| 目录 | `/opt/stacks/snet` | `/volume1/docker/snet` |
| 配置 | `/etc/snet` | `/volume1/docker/snet/etc` 或 ContainerManager 重定向路径 |
| 镜像 | registry pull 或离线 tar | registry pull 或离线 tar |
| WG 端口 | 52100 | 52101 |
| Web | `http://<A_IP>:8081` | `http://<NAS_IP>:8081` |

## 12. 安全说明

- Registry：`66.187.6.46:5000`，basic auth；密码在 VPS `/opt/snet-registry/auth/password.txt`，仅管理员可见。
- 公网访问 5000/tcp 需 Hostodo 防火墙放行。
- 客户端需 `insecure-registries`（registry 是 HTTP）。
- 合并镜像内 nginx 只代理 `127.0.0.1:19432`；ctl 不暴露公网。
- 不提交 `ctl-token`、`device.id`、config、registry 密码或镜像 tar。
