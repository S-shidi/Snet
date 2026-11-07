# SNET 部署记录

记录各环境当前运行的版本和部署方式。不要在此写入 token、授权码、私钥或密码。

最后更新：2026-10-27

| 环境 | 角色 | 当前版本 | 产物/方式 | 备注 |
|---|---|---|---|---|
| VPS | 仅协调服务端 | v1.3.0（`/healthz`，commit `8359cb7`） | `server` 二进制 + systemd | 2026-10-27 升级到 v1.3.0；Relay v2 未启用，旧 relay 池正常。备份 `/usr/local/snet/backups/1.3.0-20261027_015222` |
| SDserver | 客户端 | v1.0.3 | Docker 镜像 | 生产网络 `W4NSYT6E` |
| NAS | 客户端 | v1.3.0（commit `8359cb7`，build `20261027-2`） | runtime tar + sha256 | `/srv/snet-runtime/snet-runtime-1.3.0-r2.tar.gz`，sha256 `dd63bba8…366011`；容器 healthy、`snetd 1.3.0 (8359cb7)`、NAS 节点在线 |
| Mac | 客户端 | 待确认 | launchd + `/usr/local/snet/bin` | 可用 `snetctl version` 确认 |
| Android | 客户端 | 0.14.0-native (`versionCode 14`) | APK | 源码 1.3.0 / 10102，debug APK 30.7M，待安装验证 |
| Windows | 客户端 | 0.1.0（旧安装包） | NSIS | 源码已到 1.3.0，1.3.0 安装包待 CI/真机验证 |

## 更新记录

每次部署后追加一行：

| 日期 | 环境 | 版本 | 操作 | 验证 |
|---|---|---|---|---|
| 2026-10-27 | VPS | v1.3.0 | 二进制升级 `server` 到 v1.3.0（release commit `8359cb7`），保留原 env/systemd，未启用 Relay v2 | `/healthz` 返回 `serverVersion=1.3.0`、`apiVersion=1`；snet-server active；8090/TCP、8091/UDP、51820/UDP、51823/UDP 正常；备份 `1.3.0-20261027_015222` |
| 2026-10-27 | VPS | v1.2.0 | 二进制升级 `server` 到 v1.2.0（`88dd696`），保留原 env/systemd 配置，未启用 Relay v2 | `/healthz` 返回 `serverVersion=1.2.0`，8090/8091/51820 正常，旧 relay 继续转发 NAS 流量；网络 `W4NSYT6E` 仅 NAS 在线，phone/SDserver 约 33h 未上报 |
| 2026-10-26 | VPS | v1.0.0 | 卸载客户端容器 `snet-web`/旧镜像，移除 `snetd`/`snetctl`、`/snet`、节点 `CJEB5UBZ` 和设备 `sdvps`；服务端保持运行 | `/healthz` 200、8090/8091/51820 监听、无 19432/52100、网络剩余 3 节点（NAS/phone/SDserver）；备份 `/root/snet-client-removal-20261026_162417` |
| 2026-10-25 | VPS / SDserver | v1.0.3 | Web 鉴权、成员子网可见性、Windows 打包修复 | `/healthz`、`/ctl/peers`、子网路由 |
| 2026-10-25 | NAS | 当前源码 | runtime tar + sha256 | `/ctl/status`、节点互通 |
| 2026-10-27 | NAS | v1.3.0 | 首发 runtime `snet-runtime-1.3.0.tar.gz`（build `20261027-1`） | 启动成功后发现 tar 内 nginx 配置为旧版、缺少 `X-Snet-Web: 1`，会绕过 Web session 门禁；已丢弃该产物（移至 `/root/snet-runtime-1.3.0-r1-rejected.tar.gz`） |
| 2026-10-27 | NAS | v1.3.0 | 发布修正版 `snet-runtime-1.3.0-r2.tar.gz`（sha256 `dd63bba8…366011`，含当前 nginx 配置），compose `SNET_BUILD=20261027-2` 并改用 r2 URL；DSM Project `build_stream` 重建容器 | 容器 `Up (healthy)`；日志 `[upgrade] downloading runtime`、sha256 OK、`snetd 1.3.0 (8359cb7)`；NAS 节点 `lastSeenAge=0s`；旧 compose 备份 `compose.yaml.bak-1.3.0-20261027_020932` 与 `compose.yaml.bak-1.3.0-r1-20261027_021500`；旧 tar `snet-runtime-1.2.0-backup.tar.gz` |

## 版本确认命令

```bash
# 服务端
curl -sk https://snet.uizhi.eu.org:8090/healthz

# 本机客户端
snetctl version
snetctl status | head -20

# 远程主机
ssh <host> '/usr/local/snet/bin/snetd -version'
ssh <host> '/usr/local/snet/bin/server -version'
```

## 发布后更新

发布 `vX.Y.Z` 后：

1. 按 `docs/RELEASE.md` 的部署顺序升级。
2. 用上面的命令确认每个环境的版本。
3. 在本文件追加更新记录。
4. 更新 `docs/PROJECT-ATLAS.md` 的 `state` 和变更日志。
