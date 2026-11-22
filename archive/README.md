# 归档目录

这里存放不再参与构建、发布或日常开发的历史文件：

- `desktop-mac-tauri/`：早期 Tauri v2 macOS 客户端，已被 `desktop-mac-v3/`（Wails v3）取代。
- `snet-mac-fix.tar.gz`：历史 macOS 修复包，不含当前构建或发布流程所需内容。

约定：

- 归档目录不参与 CI、构建脚本和发布产物。
- 需要恢复历史实现时，先确认它与当前 `cmd/`、`internal/`、`shared/web/` 的契约是否仍然兼容。
- 新归档内容请在本文件补充一行说明。
