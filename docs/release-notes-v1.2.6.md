<!-- release-notes-v1.2.6 -->

## v1.2.6

### 客户端

- **全平台一键升级**：「设置 → 关于与更新」新增检查更新与一键升级流程：
  - 后端 `/ctl/status` 返回 `version`、`platform` 与 `upgradeSupported`，新增 `GET /ctl/check-update`（支持 GitHub API 与 `ghproxy.net` 自动回退）及下载校验基础设施。
  - **macOS**：自动下载 `.dmg`、挂载并备份覆盖 `/Applications/Snet.app`，失败自动回滚，成功后通过 `open` 自动重启。
  - **Windows**：自动下载并校验 `.exe` 安装器（含 SHA-256 校验），以 `/S` 静默运行 NSIS 覆盖安装并自动重启。
  - **Android**：新增 `REQUEST_INSTALL_PACKAGES` 权限与 `SnetAndroid.downloadAndInstallApk`，流式回报进度并通过 `FileProvider` 拉起系统安装器。
  - **Docker / Linux Web**：检测到新版本时展示宿主机 `docker compose pull && docker compose up -d` 高亮提示与一键复制按钮。
- **玩客云 OpenWrt 固件**：预置固定访问域名 `snet.lan -> 127.0.0.1`（`dnsmasq.conf` + UCI `dhcp` domain），放行 nginx `server_name`，插上网线访问 `http://snet.lan` 即可打开 SNET 控制台。
- Android `versionCode` 升至 15。

### 产物

- 客户端 `v1.2.6`：Android APK、macOS DMG、Windows 安装器/压缩包、玩客云线刷固件、全平台二进制包、GHCR/ACR 容器镜像 (`v1.2.6` / `latest`)。
