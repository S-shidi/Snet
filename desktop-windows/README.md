# Snet

SNET Windows 客户端（Wails v3 GUI 壳 + `snetd` + Wintun）。第一阶段通过 UAC 提权启动 `snetd.exe`，复用共享 Web UI。

## 目录

```text
desktop-windows/
├── main.go                  Wails 应用、窗口、原生托盘
├── service.go               UAC 提权启动 snetd、ctl 代理
├── assets/tray.png          托盘图标
├── frontend/build-frontend.sh
├── scripts/build-windows.sh 交叉编译并组装运行目录
├── build/                   Wails 构建配置
└── Taskfile.yml
```

## 运行文件

GUI 启动时会在自身目录查找：

```text
Snet.exe
snetd.exe
wintun.dll
```

`snetd.exe` 必须和 `wintun.dll` 位于同一目录。GUI 使用
`powershell Start-Process -Verb RunAs` 启动 snetd，并传入绝对路径：

```text
-config C:\ProgramData\SNET\config.json
```

配置、`ctl-token`、启动日志都位于 `C:\ProgramData\SNET`。

## 构建

从仓库根目录执行：

```bash
./desktop-windows/scripts/build-windows.sh
```

脚本会：

1. 交叉编译 `snetd.exe`、`snetctl.exe`
2. 下载并校验 `wintun.dll`
3. 构建共享 Web UI
4. 执行 `wails3 build GOOS=windows GOARCH=amd64`
5. 将运行文件复制到 `desktop-windows/bin/`
6. 执行 `wails3 package` 生成 NSIS 安装包
7. 生成 `dist/Snet-Setup-v1.2.5.exe`
8. 生成 `dist/Snet-v1.2.5-win-Setup.zip` 和 `dist/Snet-v1.2.5-win-portable.zip`

产物：

```text
desktop-windows/bin/Snet.exe
desktop-windows/bin/snetd.exe
desktop-windows/bin/snetctl.exe
desktop-windows/bin/wintun.dll
dist/Snet-Setup-v1.2.5.exe
dist/Snet-v1.2.5-win-Setup.zip
dist/Snet-v1.2.5-win-portable.zip
```

## 安装运行

运行 `dist/Snet-Setup-v1.2.5.exe`。安装器会写入：

```text
C:\Program Files\SNET\Snet.exe
C:\Program Files\SNET\snetd.exe
C:\Program Files\SNET\wintun.dll
```

并注册自动启动的 `SNetDaemon` 服务、创建桌面和开始菜单快捷方式。之后从快捷方式
启动 GUI 不再需要 UAC。

正式版通过安装包安装 `SNetDaemon` Windows 服务；服务启动无需 UAC。
便携目录仅用于调试，运行前需要先由安装包注册服务。

Windows 10/11 需要 WebView2 Runtime（系统通常已预装）。
