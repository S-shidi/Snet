# macOS Wails 客户端开发排坑记录

本文记录 SNET macOS Wails 客户端在开发、签名、提权和打包过程中遇到的问题与当前解决方案。

## 1. systray 导致启动 SIGTRAP

**现象**

Wails v2 同时引入 `getlantern/systray`、`efeenesc/systray` 或 `fyne.io/systray` 时，应用启动即崩溃，macOS 报 SIGTRAP。

**原因**

Wails 与 systray 都要求占用 macOS 主线程/NSApplication 事件循环，在 macOS 26+ 上发生 UI 主线程冲突。

**当前方案**

- 完全移除 systray
- 应用改为普通窗口模式启动
- 关闭窗口即退出
- 后续如果需要菜单栏能力，优先评估 Wails v3 原生 SystemTray，而不是继续在 v2 中叠加第三方托盘库

## 2. AppleScript 提权路径空格

**现象**

`snetd` 的 `-config` / `-device-id-file` 被截断，启动后读取了错误配置。

**原因**

路径中包含 `Application Support` 空格，直接拼接 shell 命令会导致参数被拆分。

**解决方案**

AppleScript 中所有路径都使用 `quoted form of`：

```applescript
do shell script "..." & quoted form of configPath & "..." with administrator privileges
```

不要手工拼接未转义路径。

## 3. osascript 中 nohup 报错

**现象**

```text
nohup: can't detach from console
```

**原因**

通过 `osascript` / `do shell script` 启动 `nohup` 时，进程没有可用的控制终端，`nohup` 无法按预期 detach。

**解决方案**

- 移除 `nohup`
- 直接使用后台 `&`
- 保留日志重定向：

```applescript
" >> " & quoted form of logPath & " 2>&1 &"
```

## 4. SIP 保护目录

**现象**

以 root 启动 `snetd` 时无法可靠写入 `~/Library/Application Support/virtual-net/`，或者用户权限与 root 权限互相踩踏。

**原因**

用户目录受 macOS SIP/TCC 保护，root 进程和登录用户进程对同一路径的访问语义不一致。

**解决方案**

配置统一迁移到：

```text
/usr/local/etc/snet/
```

提权脚本负责创建并设置权限：

```applescript
mkdir -p /usr/local/etc/snet
chown root:wheel /usr/local/etc/snet
chmod 755 /usr/local/etc/snet
```

文件：

```text
/usr/local/etc/snet/config.json
/usr/local/etc/snet/device.id
/usr/local/etc/snet/ctl-token
/usr/local/etc/snet/snetd.log
```

旧目录 `~/Library/Application Support/virtual-net/` 在启动时通过同一次提权脚本清理。

## 5. App 必须自包含

**规则**

- 只允许使用 `SnetMac.app/Contents/Resources/snetd`
- 不搜索 `/usr/local/snet/bin/snetd`
- 不搜索系统 `PATH`
- 不复用系统级 launchd 的 `snetd`
- 只复用由同一个 App 包、同一个 `/usr/local/etc/snet/config.json` 启动的进程

缺少内置二进制时直接报错：

```text
应用包损坏，缺少 snetd 核心
```

## 6. 打包与发布

GitHub Actions `Build macOS App` 会：

1. 构建 `snetd-darwin-arm64`
2. 构建 Wails App
3. 注入 `snetd` 到 App Resources
4. Ad-hoc 签名
5. 生成 `.zip`
6. 生成 `.dmg`
7. 上传带版本和 commit 的 artifact

产物命名：

```text
SnetMac-v<VERSION>-<SHORT_SHA>.zip
SnetMac-v<VERSION>-<SHORT_SHA>.dmg
```

## 7. LaunchDaemon 自动提权

首次启动只提权一次，安装：

```text
/Library/LaunchDaemons/com.snet.daemon.plist
/usr/local/bin/snetd
/usr/local/etc/snet/
```

plist 按需启动，不设置 `RunAtLoad` / `KeepAlive`。App 启动时检测 19432 端口，端口不通则：

```bash
launchctl kickstart -k system/com.snet.daemon
```

App 退出时先请求 `/ctl/shutdown`，再清理 launchd job 和残留 `snetd` 进程。安装脚本位于 `desktop-mac-v3/scripts/install-daemon.sh`，构建时必须复制到 App 的 `Contents/Resources/`。

## 8. 托盘最终移除

`fyne.io/systray` 在 macOS 26 上仍然会触发 SIGTRAP。当前结论：

- 不再使用任何第三方 systray
- 应用恢复普通窗口启动
- 关闭窗口直接退出
- LaunchDaemon 继续负责 `snetd` 的免密启动
- 如需菜单栏，等待 Wails v3 原生 SystemTray 后再评估

## 9. macOS 26 图标白框

**现象**

在 macOS 26（Tahoe）启动台 / 访达中，Snet 的蓝色图标被缩小，外面多了一圈浅色底板。

**原因**

Tahoe 只原生渲染 Icon Composer 的 `.icon` 图标。只提供旧式 `.icns` 时，系统会检测到图标边缘的透明像素，把旧图标缩小后放进系统生成的浅色底板里。`build/appicon.png` / `icons.icns` 本身是满幅蓝色，但系统仍会这样处理。

**解决方案**

用仓库里的 `build/appicon.icon` 编译出 `Assets.car`，并在 `Info.plist` 声明：

```xml
<key>CFBundleIconName</key>
<string>appicon</string>
```

- 本地 macOS 构建：`wails3` 的 `common:generate:icons` 任务会调用 actool 生成 `build/darwin/Assets.car`，打包任务会把它复制进 `Contents/Resources/`。
- GitHub Actions：`Build macOS App` 工作流使用 `xcrun actool` 从 `build/appicon.icon` 生成 `Assets.car`。
- 编译 `.icon` 需要 Xcode 26 / macOS 26 SDK；`Assets.car` 是构建产物，不提交到仓库。
- `icons.icns` 继续保留，作为 macOS 15 及更早版本的图标和 DMG 卷图标。

**注意：logo 图层用 PNG，不用 SVG**

白色主图形放在 `build/appicon.icon/Assets/snet_icon_logo.png`（由 `snet_icon_vector.svg` 渲染出的透明 PNG），`icon.json` 引用这个 PNG。原因是 Icon Composer 的 SVG 渲染器不会把父级 `<g fill="#FFFFFF">` 继承给子 `<path>`，编译后白色图形会消失，只剩带自身 `fill` 的绿点。修改 logo 后需要重新从 SVG 生成该 PNG。
