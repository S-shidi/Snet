# Synology Docker MCP 安装完成

> ⚠️ **安全告警（已脱敏）**：本文档早期版本曾把 NAS 管理员密码明文写在本文件中，并随 git
> 提交过。密码已替换为占位符。**该密码视为已泄露，请在群晖上立即轮换**（控制面板 → 用户与群组）。
> git 历史中的旧版本同样需要清理，单纯改当前文件不足以消除泄露。

## ✅ 已完成配置

### 1. MCP 服务器配置

已添加到 `~/.hermes/config.yaml`:

```yaml
mcp_servers:
  synology-docker:
    command: "npx"
    args:
      - "-y"
      - "synology-docker-mcp"
    env:
      NAS_HOST: "10.7.86.111"
      NAS_PORT: "8688"
      NAS_USER: "S.shidi"
      NAS_PASSWORD: "<从环境变量注入，不要写入仓库>"
      NAS_DOCKER_DIR: "/volume1/docker"
    timeout: 120
    connect_timeout: 60
```

> 密码改用环境变量 `NAS_PASSWORD` 注入。`opencode.jsonc` 含本机/NAS 信息，
> 已加入 `.gitignore`；任何明文口令都不能写进配置文件或文档。

### 2. 依赖检查

- ✓ MCP SDK 已安装
- ✓ Node.js v22.22.3
- ✓ npx 10.9.8

---

## 🔄 下一步：重启 Hermes

**必须重启 Hermes 桌面应用才能加载新的 MCP 工具**

### 方法 1: 完全退出并重启

```bash
# macOS: 在 Hermes 菜单中选择"退出"
# 或按 Cmd+Q

# 然后重新打开 Hermes
```

### 方法 2: 如果在聊天中，可以使用命令

```
/reload
```

（注：某些配置更改需要完全退出才能生效）

---

## 📦 安装后可用工具

重启后，Hermes 将自动连接到 Synology Docker MCP 并注册以下工具（前缀为 `mcp_synology_docker_`）：

| 工具 | 功能 |
|------|------|
| `synology_docker_ps` | 查看所有运行中的容器 |
| `synology_docker_logs` | 获取容器日志 |
| `synology_docker_manage` | 控制容器（启动/停止/重启/删除） |
| `synology_project_list` | 发现 Docker Compose 项目 |
| `synology_project_manage` | 管理 Compose 项目 |
| `synology_dsm_project_list` | DSM 原生 Project 列表 |
| `synology_dsm_project_manage` | 管理 DSM Project |
| `synology_read_file` | 读取配置文件 |
| `synology_write_file` | 修改配置文件 |

---

## 🎯 使用示例

重启后，我可以直接帮你：

### 示例 1: 检查容器状态

```
"检查群晖 NAS 上的 SNET 容器状态"
```

### 示例 2: 查看容器日志

```
"查看群晖 NAS 上 snet-client 容器的日志"
```

### 示例 3: 重启容器

```
"重启群晖 NAS 上的 snet-client 容器"
```

### 示例 4: 更新 Docker Compose 项目

```
"更新群晖 NAS 上的 SNET 项目"
```

---

## 🔒 安全说明

Synology Docker MCP 通过 SSH 安全连接到群晖 NAS：

- ✓ 不暴露 Docker TCP 端口
- ✓ 自动提权处理 `sudo`
- ✓ 所有操作通过 SSH 加密
- ✓ 密码通过环境变量注入，不进仓库

> 该 MCP 具备启停/删除容器、读写 NAS 上 `.env` 与 `docker-compose.yml` 的能力。
> 本项目的 snetd 客户端运行在这台 NAS 上，误操作会直接中断 WireGuard 隧道。

---

## ✅ 验证安装

重启后，我可以执行以下命令验证：

```bash
# 从 Mac 验证
mcp_synology_docker_ps
```

如果看到容器列表，说明 MCP 工具已成功加载！

---

## 📚 参考文档

- GitHub: https://github.com/hifishhe/Synology-Docker-MCP
- Hermes MCP 文档: `~/.hermes/skills/mcp/native-mcp/SKILL.md`

---

**现在请重启 Hermes 桌面应用！**