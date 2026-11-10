# macOS Wails 客户端开发排坑记录

本文记录 SNET macOS Wails 客户端在开发、签名、提权和打包过程中遇到的问题与当前解决方案。

## 1. systray 导致启动 SIGTRAP

**现象**

Wails v2 同时引入 `getlantern/systray` / `efeenesc/systray` 时，应用启动即崩溃，macOS 报 SIGTRAP。

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
