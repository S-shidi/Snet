# 部署环境与路径

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
