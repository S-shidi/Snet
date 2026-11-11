# SnetMac v3 — Wails v3 macOS 客户端

这是 `feat/wails-v3` 分支的 Wails v3 客户端骨架，使用原生 `SystemTray`，保留 LaunchDaemon 免密启动逻辑。

## 结构

```text
desktop-mac-v3/
├── main.go                 Wails v3 应用、窗口、原生托盘
├── service.go              LaunchDaemon / ctl 代理 Service
├── scripts/install-daemon.sh
├── assets/tray.png         18–22px 模板图标
├── frontend/
│   ├── build-frontend.sh   从 shared/web 构建静态资源
│   └── dist/               index.html / styles.css / app.js
├── build/config.yml
└── Taskfile.yml
```

## 功能

- 原生 `app.SystemTray.New()` 菜单栏
- 首次启动安装 LaunchDaemon：

```text
/Library/LaunchDaemons/com.snet.daemon.plist
```

- 后续启动：

```text
launchctl kickstart -k system/com.snet.daemon
```

- 配置固定：

```text
/usr/local/etc/snet/config.json
/usr/local/etc/snet/device.id
/usr/local/etc/snet/ctl-token
/usr/local/etc/snet/snetd.log
```

- 双击启动时直接显示主窗口
- 关闭主窗口隐藏到菜单栏，不退出 App
- 托盘菜单：打开控制面板 / 退出 SNET
- 退出 SNET 会关闭 `snetd`，下次启动按需重新拉起
- 前端通过 Go `ctlProxy` 访问 `127.0.0.1:19432`

## 构建

```bash
cd desktop-mac-v3
wails3 task darwin:build-only
xattr -cr bin/SnetMac.app
codesign --remove-signature bin/SnetMac.app/Contents/MacOS/SnetMac 2>/dev/null || true
codesign --remove-signature bin/SnetMac.app/Contents/Resources/snetd 2>/dev/null || true
plutil -lint bin/SnetMac.app/Contents/Info.plist
codesign --force --sign - bin/SnetMac.app/Contents/Resources/snetd
codesign --force --sign - bin/SnetMac.app/Contents/MacOS/SnetMac
codesign --force --sign - bin/SnetMac.app
codesign --verify --strict bin/SnetMac.app
```

Taskfile 的 `build-only` 会：

1. 从 `shared/web` 构建 `frontend/dist`
2. 编译 Wails v3 App
3. 将 `../dist/snetd-darwin-arm64` 和 `scripts/install-daemon.sh` 复制到 App Resources
4. 生成未签名的 `bin/SnetMac.app`

签名和 DMG 打包由 CI 手工完成，避免 Wails 自动签名后再修改 App 内容。

产物在：

```text
bin/SnetMac.app
```

## 彻底卸载 LaunchDaemon

```bash
sudo launchctl bootout system /Library/LaunchDaemons/com.snet.daemon.plist 2>/dev/null || true
sudo rm -f /Library/LaunchDaemons/com.snet.daemon.plist
sudo killall -9 snetd 2>/dev/null || true
```

## 开发

```bash
cd desktop-mac-v3
wails3 task darwin:run
```

或直接：

```bash
wails3 build
```
