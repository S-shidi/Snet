# SnetMac — macOS 菜单栏客户端

基于 **Wails v2** 的 macOS 原生 GUI 骨架。前端复用仓库根目录的 `shared/web`，Go 后端负责启动/复用 `snetd`、代理 `/ctl/*`，并通过 `efeenesc/systray` 提供菜单栏入口。

> Wails v2 没有内置 system tray API，因此托盘使用 `github.com/efeenesc/systray`。如果遇到 Wails/托盘主线程冲突，见文末说明。

## 环境要求

- macOS 13+
- Xcode Command Line Tools：`xcode-select --install`
- Go 1.25+
- Node.js 20+ / npm
- Wails v2 CLI：

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
wails doctor
```

## 目录

```text
desktop-mac/
├── main.go                 Wails 入口、托盘、窗口
├── app.go                  snetd 启动/复用、ctl-token 代理
├── wails.json              Wails 配置
├── frontend/               WebView 资源
│   ├── build-frontend.sh   从 shared/web 同步并 esbuild
│   ├── index.html
│   ├── styles.css
│   └── app.js
├── assets/tray.png         菜单栏模板图标
└── build/appicon.png       应用图标
```

## 前端

`frontend` 是 `shared/web` 的构建副本。修改 `shared/web` 后执行：

```bash
cd desktop-mac
./frontend/build-frontend.sh
```

`wails.json` 已配置：

- `assetdir`: `frontend`
- `frontend:build`: `./build-frontend.sh`
- `frontend:dev:build`: `./build-frontend.sh`

WebView 内的相对 `/ctl/*` 请求由 `app.go` 的 `ctlProxy` 转发到：

```text
http://127.0.0.1:19432/ctl/*
```

代理会读取 `~/Library/Application Support/virtual-net/ctl-token`，并注入 `X-Ctl-Token`，因此不需要在 Web UI 中暴露 token，也没有 CORS 问题。

## 启动 snetd

应用启动时：

1. 通过 `pgrep` 检查是否已有 `snetd`。
2. 如果没有，查找 `SNETD_BIN`、应用 `Contents/Resources/snetd`、`/usr/local/snet/bin/snetd` 或开发目录 `dist/snetd-darwin-arm64`。
3. 使用 AppleScript：

```applescript
do shell script "snetd ..." with administrator privileges
```

macOS 会弹出系统管理员授权框；`snetd` 以 root 启动，以便创建 `utun` 和配置路由。

配置与身份文件使用当前用户配置目录：

```text
~/Library/Application Support/virtual-net/config.json
~/Library/Application Support/virtual-net/device.id
~/Library/Application Support/virtual-net/ctl-token
~/Library/Application Support/virtual-net/snetd.log
```

如果系统已有 launchd 管理的 `snetd`，应用会直接复用，不会重复启动。

## 开发

先构建 macOS arm64 daemon：

```bash
cd /path/to/Snet
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -o dist/snetd-darwin-arm64 ./cmd/client/snetd
```

然后：

```bash
cd desktop-mac
wails dev
```

开发模式默认会自动查找 `../dist/snetd-darwin-arm64`。也可以显式指定：

```bash
SNETD_BIN="$PWD/../dist/snetd-darwin-arm64" wails dev
```

## 构建

```bash
cd desktop-mac
wails build -platform darwin/arm64
```

把 daemon 放进应用包：

```bash
cp ../dist/snetd-darwin-arm64 build/bin/SnetMac.app/Contents/Resources/snetd
chmod +x build/bin/SnetMac.app/Contents/Resources/snetd
codesign --force --deep --sign - build/bin/SnetMac.app
open build/bin/SnetMac.app
```

最终应用是一个菜单栏应用：

- 点击菜单栏图标打开菜单
- `打开控制面板` 显示 WebView
- `隐藏控制面板` 收回菜单栏
- `退出 SNET` 退出应用

窗口关闭按钮默认隐藏到菜单栏，不退出进程。

## 注意事项

- Wails v2 没有官方 system tray API；当前使用 `efeenesc/systray`。
- 如果菜单栏图标不出现，先确认 `wails doctor` 正常，并尝试将 `main.go` 中的托盘启动方式改为在 `main` 运行托盘、Wails 放到 goroutine；不同 macOS/Wails 版本对主线程调度敏感。
- `snetd` 需要 root；不要在 WebView 内直接执行特权命令。
- 不要提交 `ctl-token`、`device.id`、`config.json` 或私钥。
