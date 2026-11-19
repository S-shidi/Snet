# 客户端部署入口

两种方式二选一：

## 方案 1：Docker Compose（推荐）

```text
deploy-prod/docker-client/README.md
```

- 标准 `compose.yaml`
- 镜像从阿里云 ACR 拉取，例如 `crpi-0ro5v5xbjp4autp9.cn-hangzhou.personal.cr.aliyuncs.com/snet/snet-client:latest`
- 不再需要 `-f compose.client.yaml`，也没有 build 段；VPS 私有 Registry 已废弃
- 客户端 A：标准 Linux `/opt/snet-deploy/deploy-prod/docker-client`
- 客户端 B：NAS `/volume1/docker/snet/deploy-prod/docker-client`

## 方案 2：二进制 + systemd（无 Docker 备选）

```text
deploy-prod/manual-client/README.md
```

使用 `dist-deploy/` 的 `snetd`/`snetctl` + `snetd.service`。

客户端 A/B 均为手动部署，不使用 Docker MCP 自动部署。
