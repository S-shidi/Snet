# SnetMac — macOS Wails 客户端

基于 **Wails v2** 的 macOS 原生 GUI。前端复用仓库根目录的 `shared/web`，Go 后端负责启动/复用 `snetd`，并代理 `/ctl/*`。

> 重要：当前版本移除了 `getlantern/systray` / `efeenesc/systray`，以普通窗口模式启动。Wails v2 与第三方 systray 在较新 macOS 上存在主线程冲突，会导致启动即 SIGTRAP 崩溃。

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
├── main.go                 Wails 入口
├── app.go                  snetd 启动/复用、ctl-token 代理
├── wails.json              Wails 配置
├── frontend/               WebView 资源
│   ├── build-frontend.sh   从 shared/web 同步并 esbuild
│   ├── index.html
│   ├── styles.css
│   └── app.js
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

代理会读取：

```text
~/Library/Application Support/virtual-net/ctl-token
```

并注入 `X-Ctl-Token`，因此不需要在 Web UI 中暴露 token，也没有 CORS 问题。

## 启动 snetd

应用启动时：

1. 通过 `pgrep` 检查是否已有 `snetd`。
2. 如果没有，查找 `SNETD_BIN`、应用 `Contents/Resources/snetd`、`/usr/local/snet/bin/snetd` 或开发目录 `dist/snetd-darwin-arm64`。
3. 使用 AppleScript：

```applescript
do shell script "nohup snetd ..." with administrator privileges
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

应用以普通窗口启动；关闭窗口即退出应用。

## 注意

- 当前不使用系统托盘，避免 Wails v2 与 systray 的主线程冲突。
- `snetd` 需要 root；不要在 WebView 内直接执行特权命令。
- 不要提交 `ctl-token`、`device.id`、`config.json` 或私钥。
