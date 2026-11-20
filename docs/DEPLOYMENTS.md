# 部署环境与路径

## 当前生产拓扑（v1.1.15）

业务网络：`ARF38UYW`（`HomeNET`，`10.88.0.0/24`），Relay v2 入口：
`snet.uizhi.eu.org:8092`。

| 角色 | 位置 | 地址 / 网段 | 版本 | 当前状态 |
|---|---|---|---|---|
| 协调服务端 + Relay v2 | VPS `66.187.6.46` | `snet.uizhi.eu.org:8090` / `:8092` | `v1.1.15` | 正常，Relay 会话稳定 |
| VPS Hub 容器 | VPS `/opt/snet-hub` | `snet-hub` | `v1.1.15` | 容器在线；当前未加入业务网络 |
| sdserver | Docker 主机 | `10.10.11.11`，`10.10.11.0/24` 网关 | `v1.1.15` | 在线，member，与 SDNAS/MacBook 直连 |
| SDNAS | 群晖 NAS | `10.7.86.111`，`10.7.86.0/24` 网关 | `v1.1.15` | 在线，`natType=cone`，与 sdserver 直连 |
| MacBook | 家庭 LAN | `10.88.0.75` | `v1.1.15` 发布包 | 在线，owner；升级后自动同步待审批请求 |
| SHIDI-PC | Windows | `10.88.0.77` | `v1.1.15` 发布包 | 当前离线 |
| Android | 移动网络 | `10.88.0.93` | `v1.1.15` 发布包 | 当前离线 |

说明：

- 当前在线节点均通过 Relay v2 建链；`sdserver`、`SDNAS`、`MacBook` 之间的 `peerDirect`
  显示 `path=direct`，其余离线节点上线后会按同一策略探测直连。
- VPS Hub 容器保持 `v1.1.15`，但截至本记录未加入 `ARF38UYW`；业务中继由
  VPS 上的 `snet-relay` 承担。
- 群晖 NAS 通过 `10.7.86.0/24` 网关向网络暴露家庭 LAN；sdserver 通过
  `10.10.11.0/24` 暴露其所在 LAN。

## 标准路径

| 用途 | 路径 |
|---|---|
| Docker Compose 部署目录 | `/opt/stacks/snet` |
| 群晖 NAS 客户端 | `/volume1/docker/snet` |
| 服务端数据卷 | `snet-data` → `/data` |
| 客户端数据卷 | `/etc/snet` |

## 服务端

```bash
cd /opt/stacks/snet
docker compose --env-file .env up -d
```

当前 VPS 兼容旧目录 `/opt/snet-deploy`；新部署统一使用 `/opt/stacks/snet`。

## 客户端

Docker 主机：

```bash
cd /opt/stacks/snet
docker compose pull
docker compose up -d
```

群晖 NAS（SSH 端口 `8688`）：

```bash
ssh -p 8688 <user>@<nas-ip>
cd /volume1/docker/snet
docker compose pull
docker compose up -d
```

Web UI：

```text
http://<主机IP>:8081
```

## 版本验证

```bash
curl -fsS https://snet.uizhi.eu.org:8090/healthz
docker exec snetd /usr/local/bin/snetd -version
```
