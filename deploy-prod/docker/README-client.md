# 客户端部署已改为手动

客户端 A/B 均手动部署，不再使用 Docker MCP 自动部署。

请使用：

```text
deploy-prod/manual-client/README.md
```

该目录包含：

- `config.json.example`
- `snetd.service`
- `check-prereq.sh`
- `SHA256SUMS`

`dist-deploy/` 中的 linux-amd64/arm64 `snetd`、`snetctl` 由构建机生成，不提交。
本目录的 `compose.client.yaml` / `Dockerfile.client` 仅保留为可选 Docker 参考，不是主部署路径。
