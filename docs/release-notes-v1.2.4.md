# Snet v1.2.4 release notes

Paste the block below into the GitHub Release body for `v1.2.4`.

---

## Breaking Change

从 `v1.2.4` 开始，客户端 Docker 镜像 Tag 统一为 `vX.Y.Z`（带 `v` 前缀，与 Git tag 相同）。
不再推送无 `v` 前缀的镜像 Tag。

请更新 compose 文件中的镜像地址：

```diff
- image: ghcr.io/s-shidi/snet-client:1.2.3
+ image: ghcr.io/s-shidi/snet-client:v1.2.4
```

服务端镜像（`snet-snetserv`、`snet-admin-server`、`snet-relay`）同样只使用 `vX.Y.Z`。
`latest` 仅跟随最新稳定版；预发布 tag（如 `v1.2.4-rc1`）不会更新 `latest`。

## 其他

- 发布流水线：预发布 tag 只构建 Docker 镜像，不产出 macOS / Windows / Android 资产。
- 客户端功能与 `v1.2.3` 相同，本版本只统一镜像 Tag 规则并更新文档。
