# SNET 版本与发布

本文件是发布流程的唯一操作入口。代码/测试优先于本文；如果流程与工作流文件冲突，
以 `.github/workflows/` 为准并修正本文。

## 1. 版本规则

- 使用 SemVer：`vMAJOR.MINOR.PATCH`，例如 `v1.1.0`。
- 所有组件跟随同一个产品版本：Go 服务端、`snetd`、`snetctl`、桌面端、Android、
  Docker 镜像。
- `protocol.APIVersion` 是兼容性契约，独立于产品版本；只有 wire/API 不兼容时才递增。
- 预发布用 `vX.Y.Z-rc.N`；预发布不更新 Docker `latest`。
- `VERSION` 是唯一人工维护的版本源；`internal/buildinfo.Version`、桌面端、
  Android、Docker 构建参数都必须与它一致。
- Android `versionCode` 必须单调递增，不直接从 SemVer 推导；版本变化时
  `scripts/bump-version.sh` 会加 1；同一版本重跑不会重复加。

## 2. 分支与 tag

- `main` 保持可发布；功能通过 PR 合入。
- 需要稳定期时从 `main` 切 `release/X.Y`，RC 和正式 tag 从该分支打。
- 热修复从问题版本 tag 切 `hotfix/X.Y.Z`，修复后打 patch tag，并回合 `main`。
- tag 必须是 annotated tag，禁止移动或复用：

```bash
git tag -a v1.1.0 -m "release v1.1.0"
git push origin v1.1.0
```

- 不要补打历史 `v1.0.3`：它是未提交源码部署的热修复，补 tag 会制造假历史。

## 3. 发布前准备

1. 确认工作区干净：

```bash
git status --porcelain
```

2. 更新版本和 CHANGELOG：

```bash
./scripts/bump-version.sh 1.1.0
./scripts/check-version.sh
```

3. 检查 `scripts/bump-version.sh` 生成的发布说明 `docs/releases/vX.Y.Z.md`，至少包含：
   - Added / Changed / Fixed / Security
   - 升级顺序
   - 兼容性（产品版本、`APIVersion`、最低客户端）
   - 已知问题

4. 跑完整验证：

```bash
go test -race ./...
go vet ./...
go build ./...
./scripts/build-web.sh
cd desktop && npx tsc --noEmit
cd android && ./gradlew testDebugUnitTest --no-daemon
./scripts/project-audit.sh
```

5. 本地构建发布产物：

```bash
./scripts/build-release.sh
```

产物在 `dist/`，包含 Linux/macOS/Windows 的客户端、Linux 服务端和
`checksums.txt`。

## 4. 打 tag 后自动流程

推送 `v*` tag 会触发 `.github/workflows/release.yml`：

1. 校验 `VERSION == tag`。
2. 跑 Go 测试、vet、build。
3. 构建并上传版本化 Go 二进制。
4. 调用 Windows workflow 构建 NSIS 安装包。
5. 调用 Android workflow：`gomobile bind` + Gradle，tag 构建签名 release APK。
6. 生成 checksums。
7. 构建并推送 GHCR 镜像：
   - `ghcr.io/<owner>/snetd:<version>`
   - `ghcr.io/<owner>/snet-web:<version>`
   - 稳定版额外更新 `latest`
8. 创建 GitHub Release，附件来自 `release-assets/`，说明来自
   `docs/releases/vX.Y.Z.md`。

Android 签名 Secrets：

| Secret | 用途 |
|---|---|
| `ANDROID_KEYSTORE_BASE64` | base64 编码的 release keystore |
| `ANDROID_KEYSTORE_PASSWORD` | keystore 密码 |
| `ANDROID_KEY_ALIAS` | key alias |
| `ANDROID_KEY_PASSWORD` | key 密码 |

没有这些 Secrets 时，tag 发布会在 Android 签名步骤失败；普通 `main` 构建只产出
debug APK。

## 5. 部署顺序

发布流程只构建产物，不自动改生产环境。部署仍需人工确认：

1. 服务端：先部署到 VPS，验证 `/healthz` 的 `serverVersion` 与 `commit`。
2. 灰度客户端：SDserver / NAS，验证 `/ctl/status` 的 `version` 和网络互通。
3. 成员主机 Docker 客户端：NAS / SDserver 的 `snet-web` / `snetd` 使用明确版本 tag 或 digest；VPS 仅服务端，不部署客户端。
4. 桌面端、Android、Windows：分发对应产物。
5. 更新 `docs/DEPLOYMENTS.md` 和 `docs/PROJECT-ATLAS.md`。

兼容的 MINOR/PATCH 版本通常可以先升级服务端，再升级客户端；`APIVersion` 不兼容时
必须协同升级，并在发布说明中写明最低客户端版本。

## 6. 回滚

- VPS：`scripts/rollout-vps.sh` 会在
  `/usr/local/snet/backups/<version>-<timestamp>/` 保存旧二进制和数据库；脚本结束会
  打印回滚命令。
- Docker：重新用上一个版本 tag 启动；保留 N-1、N-2 镜像。
- NAS：保留旧 runtime tar 和 compose 中的 sha256；按部署记录回退。
- Android/Windows：保留上一版 APK/安装包，重新安装即可。
- 涉及 bbolt 结构变更时，回滚前先恢复对应的 `snet.db` 备份。

## 7. 当前边界

- Windows 安装包尚未做 Authenticode 签名；首次运行可能出现 SmartScreen 提示。
- macOS 桌面 `.app/.dmg` 尚未纳入自动发布；Go 客户端二进制已覆盖 macOS。
- Android 当前只构建 `arm64` ABI。
- NAS 的 runtime tar / sha256 更新仍是目标机操作，不包含在 GitHub Release 自动流程中。
- 发布前必须确认 Windows 安装包和 Android 真机安装验证结果，不能只凭 CI 构建成功。
