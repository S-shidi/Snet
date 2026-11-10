# SnetMac — macOS Wails 客户端

基于 **Wails v2** 的 macOS 原生客户端。前端复用 `shared/web`，Go 后端通过 LaunchDaemon 管理内置 `snetd`。

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

## 结构

```text
desktop-mac/
├── main.go                 Wails 入口
├── app.go                  LaunchDaemon 安装/唤醒、ctl-token 代理
├── scripts/install-daemon.sh
├── assets/tray.png
├── frontend/               shared/web 构建产物
└── wails.json
```

## 首次安装：LaunchDaemon

第一次启动时，App 会通过 AppleScript 弹出一次管理员授权，执行：

```text
desktop-mac/scripts/install-daemon.sh
```

脚本会：

1. 将 App 内置的 `Contents/Resources/snetd` 安装到：

```text
/usr/local/bin/snetd
```

2. 创建：

```text
/usr/local/etc/snet/
```

3. 写入并加载：

```text
/Library/LaunchDaemons/com.snet.daemon.plist
```

4. 设置：

```text
RunAtLoad = true
KeepAlive = true
```

5. 删除旧目录：

```text
~/Library/Application Support/virtual-net/
```

以后启动 App 不再弹管理员密码，只会通过 `launchctl kickstart system/com.snet.daemon` 唤醒服务。

## 配置与身份

固定使用：

```text
/usr/local/etc/snet/config.json
/usr/local/etc/snet/device.id
/usr/local/etc/snet/ctl-token
/usr/local/etc/snet/snetd.log
```

App 不会搜索 `/usr/local/snet`、系统 `PATH` 或其他旧配置。内置 `snetd` 缺失时直接报错：

```text
应用包损坏，缺少 snetd 核心
```

## 窗口

- 应用以普通窗口启动
- 关闭窗口直接退出 App
- 不使用系统托盘/systray，避免 macOS 26+ 主线程 SIGTRAP

## 前端

修改 `shared/web` 后执行：

```bash
cd desktop-mac
./frontend/build-frontend.sh
```

Wails 环境会：

- 跳过 Web 登录/setup 页面
- 清除旧的 `snet.*` localStorage
- 通过 Go `ctlProxy` 注入 `/usr/local/etc/snet/ctl-token`
- `/ctl/*` 转发到 `http://127.0.0.1:19432`

## 构建

```bash
cd desktop-mac
wails build -platform darwin/arm64
```

把 daemon 和安装脚本放进应用包：

```bash
cp ../dist/snetd-darwin-arm64 build/bin/SnetMac.app/Contents/Resources/snetd
cp scripts/install-daemon.sh build/bin/SnetMac.app/Contents/Resources/install-daemon.sh
chmod +x build/bin/SnetMac.app/Contents/Resources/snetd
chmod +x build/bin/SnetMac.app/Contents/Resources/install-daemon.sh
codesign --force --deep --sign - build/bin/SnetMac.app
open build/bin/SnetMac.app
```

GitHub Actions 会自动完成上述步骤，并生成 `.zip` / `.dmg`。

## 注意

- 不要提交 `ctl-token`、`device.id`、`config.json` 或私钥。
- `snetd` 需要 root；只通过 LaunchDaemon 运行，不在 WebView 内执行特权命令。
- 开发排坑记录见 [`docs/MAC-DEV-NOTES.md`](../docs/MAC-DEV-NOTES.md)。
