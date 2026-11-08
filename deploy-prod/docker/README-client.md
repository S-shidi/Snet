# 客户端部署入口

两种方式二选一：

## 方案 1：Docker Compose（推荐，客户端 A/B）

```text
deploy-prod/docker/README-client-compose.md
```

使用 `Dockerfile.client` + `compose.client.yaml`，镜像只 COPY 预编译 `snetd`。
客户端 A 有构建环境；客户端 B 可 `docker save`/`scp`/`docker load`。

## 方案 2：二进制 + systemd（无 Docker 备选）

```text
deploy-prod/manual-client/README.md
```

使用 `dist-deploy/` 的 `snetd`/`snetctl` + `snetd.service`。

客户端 A/B 均为手动部署，不使用 Docker MCP 自动部署。
