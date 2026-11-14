# SnetWindows

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
SnetWindows.exe
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
4. 执行 `wails3 build -platform windows/amd64`
5. 将运行文件复制到 `desktop-windows/bin/`
6. 生成 `dist/SnetWindows-v1.1.5-amd64.zip`

产物：

```text
desktop-windows/bin/SnetWindows.exe
desktop-windows/bin/snetd.exe
desktop-windows/bin/snetctl.exe
desktop-windows/bin/wintun.dll
dist/SnetWindows-v1.1.5-amd64.zip
```

## 手动运行

把 `desktop-windows/bin/` 整个目录复制到 Windows 电脑，双击
`SnetWindows.exe`。首次启动由 UAC 拉起 `snetd.exe`，允许后即可创建/加入网络。

Windows 10/11 需要 WebView2 Runtime（系统通常已预装）。
