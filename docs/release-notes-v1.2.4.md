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

## 镜像与产物

- 客户端镜像同时发布到 **GHCR** 与私有 **ACR** 两个仓库，均为 `v1.2.4` + `latest`。
- 服务端三个镜像同样同时推送到 GHCR 与 ACR。
- CI 自动上传 `Snet-v1.2.4-binaries.tar.gz`（snetd/snetctl）和 WankeCloud 固件到本 Release，
  无需手动 `gh release upload`。
- 预发布 tag 只构建 Docker 镜像，不产出 macOS / Windows / Android / 固件资产。

## 其他

- 客户端功能与 `v1.2.3` 相同，本版本主要统一镜像 Tag 规则、双仓库推送与发布自动化。
