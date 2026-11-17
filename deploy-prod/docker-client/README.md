# Snet Docker 客户端部署指南

一个容器同时运行 `snetd`、Nginx 和 Snet Web 控制台。部署完成后，用浏览器打开 `http://<服务器IP>:8081` 即可创建或加入网络。

> 适合：Linux 服务器、群晖 / 威联通 NAS、软路由等 x86_64 Docker 环境。
>
> 镜像架构：`linux/amd64`。ARM 设备需要自行构建镜像。

## 1. 前提条件

- Docker 20.10+
- Docker Compose v2（使用 Compose 方式时需要）
- 宿主机存在 `/dev/net/tun`
- 至少 1 GB 内存、2 GB 可用磁盘
- `8081/tcp` 未被占用
- 如果这台机器要作为公网 WireGuard 网关，还需要一个公网 UDP 端口（默认 `51820/udp`）

检查环境：

```bash
uname -m
ls -l /dev/net/tun
docker --version
docker compose version
ss -lntup | grep ':8081' || true
```

`uname -m` 应输出 `x86_64`。如果 `/dev/net/tun` 不存在，在 Linux 上尝试：

```bash
sudo modprobe tun
```

群晖 / 威联通请在套件或内核设置中确认 TUN 已启用。

## 2. 拉取镜像

客户端镜像发布在阿里云 ACR：

```bash
export SNET_IMAGE=crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13
docker pull "$SNET_IMAGE"
```

如果提示未授权：

```bash
docker login crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com
```

使用你自己的阿里云容器镜像服务账号登录，不要把账号密码写进脚本或提交到仓库。

验证镜像：

```bash
docker image inspect "$SNET_IMAGE" --format '{{.Id}}'
```

## 3. 准备数据目录

所有持久化数据都放在一个目录里，升级或重建容器不会丢失设备身份和网络配置。

```bash
sudo mkdir -p /opt/snet/data
sudo chmod 700 /opt/snet /opt/snet/data
```

目录内容：

```text
/opt/snet/data/
├── config.json      # 可选，首次可让 Web 控制台生成/修改
├── device.id        # 设备身份，重装后必须保留
├── ctl-token        # 本机控制 API 令牌
└── snetd.log        # 运行日志
```

### 可选：预置配置文件

使用官方协调服务时，可以跳过这一步，直接在 Web 控制台里设置。

使用自建服务端时，创建 `config.json`：

```bash
sudo tee /opt/snet/data/config.json >/dev/null <<'JSON'
{
  "serverAddr": "https://<你的服务端>:8090",
  "wireguardPort": 51820,
  "networks": {},
  "keyRotationDays": 30,
  "natMapping": true
}
JSON
sudo chmod 600 /opt/snet/data/config.json
```

常用字段：

| 字段 | 说明 |
|---|---|
| `serverAddr` | 协调服务地址；官方服务为 `https://snet.uizhi.eu.org:8090` |
| `wireguardPort` | 本机 WireGuard UDP 端口，默认 `51820` |
| `natMapping` | 自动尝试 UPnP / NAT-PMP / PCP 端口映射，建议 `true` |
| `allowRelay` | 是否允许本机作为中继网关，普通成员保持 `false` |

## 4. 启动方式一：普通 Docker

如果只是单机部署，普通 `docker run` 最简单。先清理同名旧容器：

```bash
docker rm -f snetd 2>/dev/null || true
```

启动：

```bash
docker run -d \
  --name snetd \
  --restart unless-stopped \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -v /opt/snet/data:/etc/snet \
  crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13
```

为什么需要这些参数：

| 参数 | 原因 |
|---|---|
| `--network host` | Nginx 监听宿主机 `8081`，`snetd` 监听本机 `19432` 和 WireGuard UDP 端口 |
| `--cap-add NET_ADMIN` | 创建 TUN、配置路由、NAT 和中继转发 |
| `--device /dev/net/tun` | WireGuard 虚拟网卡 |
| `-v ...:/etc/snet` | 持久化设备身份、配置和令牌 |

查看状态：

```bash
docker ps --filter name=snetd
docker logs -f snetd
```

## 5. 启动方式二：Docker Compose

Compose 更适合长期维护和升级。以下步骤与普通 Docker 二选一。

创建目录：

```bash
sudo mkdir -p /opt/snet/data
cd /opt/snet
```

创建 `compose.yaml`：

```yaml
services:
  snet:
    image: ${SNET_IMAGE:-crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13}
    container_name: snetd
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./data:/etc/snet
```

创建可选 `.env`：

```dotenv
SNET_IMAGE=crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13
```

启动：

```bash
cd /opt/snet
docker compose pull
docker compose up -d
```

查看状态和日志：

```bash
docker compose ps
docker compose logs -f
```

停止：

```bash
docker compose down
```

## 6. 首次使用

浏览器打开：

```text
http://<服务器IP>:8081
```

1. 设置 Web 管理密码。
2. 如果使用自建服务端，在「设置 → 协调服务」中填写服务端地址；使用官方服务端可保持默认。
3. 点击「添加网络」：
   - 创建新网络；或
   - 粘贴别人发来的邀请链接加入网络。
4. 需要 iPhone / iPad / 标准 WireGuard 客户端接入时，在网络详情中打开「更多 → 生成 WireGuard 配置」。

首次启动会自动生成 `device.id`、`ctl-token`。之后升级、重建容器都不要删除 `/opt/snet/data`。

## 7. 端口与防火墙

| 端口 | 协议 | 用途 | 是否公网开放 |
|---|---|---|---|
| `8081` | TCP | Web 控制台 | 建议仅内网，或通过反向代理 + HTTPS |
| `51820` | UDP | WireGuard（默认） | 仅公网网关节点需要 |
| `19432` | TCP | 本机控制 API | **不要开放** |

因为使用 `--network host`，不需要写 `-p` / `ports:`。

使用云服务器时，在安全组中放行需要的端口；只作为普通成员时，通常无需向公网开放 WireGuard 端口。

## 8. 更新版本

数据目录保持不变即可。

普通 Docker：

```bash
export SNET_IMAGE=crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:v1.1.13
docker pull "$SNET_IMAGE"
docker rm -f snetd
docker run -d \
  --name snetd \
  --restart unless-stopped \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -v /opt/snet/data:/etc/snet \
  "$SNET_IMAGE"
```

Docker Compose：

```bash
cd /opt/snet
docker compose pull
docker compose up -d
```

检查版本：

```bash
docker exec snetd /usr/local/bin/snetd -version
```

## 9. 离线安装

在能访问阿里云 ACR 的机器上：

```bash
ACR=crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet
docker pull $ACR/snet-client:v1.1.13
docker save $ACR/snet-client:v1.1.13 | gzip -1 > snet-client-v1.1.13.tar.gz
scp snet-client-v1.1.13.tar.gz root@<目标机器IP>:/tmp/
```

在目标机器：

```bash
gunzip -c /tmp/snet-client-v1.1.13.tar.gz | docker load
```

然后按第 4 节或第 5 节启动即可，镜像名保持 `crpi-.../snet-client:v1.1.13`。

## 10. 卸载

普通 Docker：

```bash
docker stop snetd
docker rm snetd
```

Docker Compose：

```bash
cd /opt/snet
docker compose down
```

以上操作默认保留 `/opt/snet/data`。确认不再需要设备身份和网络配置后，才手动删除：

```bash
sudo rm -rf /opt/snet/data
```

删除后重新部署会生成新的 `device.id`，在服务端会被视为一台新设备。

## 11. 故障排查

| 现象 | 检查 |
|---|---|
| 容器启动后立即退出 | `docker logs snetd`；通常是 `/dev/net/tun` 缺失或权限不足 |
| 无法创建 TUN | 确认 `--cap-add NET_ADMIN` 和 `--device /dev/net/tun` |
| `8081` 打不开 | `ss -lntup \| grep :8081`；容器是否 running；防火墙是否放行 |
| `8081` 被占用 | 停止占用端口的程序，或修改镜像内 Nginx 配置后自行构建 |
| 忘记 Web 密码 | 停止容器，备份并删除 `data/config.json` 中的密码相关字段后重启；不要删除 `device.id` |
| 拉取镜像 401 / denied | `docker login` 阿里云 ACR，确认账号有镜像权限 |
| 服务端不可达 | `curl -fsS https://<服务端>:8090/healthz` |
| 手机连不上 | 网关节点必须有公网 UDP 入口，生成配置时选择可用的出口网关 |
| 直连不稳定 | 检查 UDP 端口、NAT 映射和防火墙；失败时会自动尝试中继 |

查看完整日志：

```bash
docker logs --tail 200 snetd
```

进入容器检查版本：

```bash
docker exec snetd /usr/local/bin/snetd -version
```

## 12. 安全建议

- `device.id`、`config.json`、`ctl-token` 包含设备身份或私密材料，不要提交到 Git，也不要发给别人。
- `8081` 是管理入口，尽量不要直接暴露到公网；需要远程访问时，使用 VPN、内网穿透或 HTTPS 反向代理。
- 不要在 `compose.yaml`、脚本或文档中写死阿里云 ACR 账号密码。
- 公网 WireGuard 网关只开放必要的 UDP 端口，`19432` 永远不要暴露。
- 定期备份 `/opt/snet/data`；恢复时保留原 `device.id`，网络身份才不会变化。

## 13. 常用命令速查

```bash
# 查看容器
docker ps --filter name=snetd

# 查看日志
docker logs -f snetd

# 重启
docker restart snetd

# 查看版本
docker exec snetd /usr/local/bin/snetd -version

# Compose 方式
cd /opt/snet
docker compose ps
docker compose logs -f
docker compose pull
docker compose up -d
docker compose down
```
