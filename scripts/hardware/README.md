# SNET OpenWrt 固件构建

YAML 驱动的多设备 OpenWrt Image Builder 框架。

## 环境依赖

Debian / Ubuntu：

```bash
sudo apt-get update
sudo apt-get install -y patch gawk unzip bzip2 zstd make gcc perl
```

openEuler / Fedora：

```bash
sudo dnf install -y patch gawk unzip bzip2 zstd make gcc perl
```

还需要 `yq`（推荐）或 Python 3 + PyYAML。脚本会优先使用 `yq`，没有时回退到 PyYAML。

## 设备矩阵

编辑 `devices.yaml`：

```yaml
devices:
  - name: "NanoPi-R5C"
    target: "rockchip/armv8"
    profile: "friendlyarm_nanopi-r5c"
    arch: "arm64"
    post_script: ""
    packages: "-luci -luci-ssl"
    imagebuilder_url: ""
```

可选字段：

- `imagebuilder_url`：覆盖自动生成的 Image Builder 下载地址
- `imagebuilder_version`：覆盖全局 OpenWrt 版本
- `extra_packages`：追加软件包
- `image_name`：额外输出的镜像名前缀

## 构建

```bash
# 查看设备
bash scripts/hardware/build-firmware.sh --list

# 单个设备
bash scripts/hardware/build-firmware.sh --device NanoPi-R5C

# 全部设备
bash scripts/hardware/build-firmware.sh --all
```

产物默认在：

```text
dist/hardware/<device>/
```

构建会把以下内容注入固件：

- `snetd` / `snetctl`
- `shared/web` 构建出的 `/www/snet`
- `/etc/init.d/snetd` procd 服务
- `/etc/config/uhttpd` 默认首页
- `/etc/uci-defaults/99-snet-setup.sh` 首次启动初始化

Image Builder 缓存默认在：

```text
~/.cache/snet-openwrt/
```

可用 `SNET_IB_CACHE=/path` 覆盖。

## NanoPi R5C

当前矩阵使用 OpenWrt `24.10.5`，Image Builder 会自动尝试 `.tar.xz`，不存在时回退到 `.tar.zst`。

生成的设备镜像：

```text
dist/hardware/nanopi-r5c/openwrt-24.10.5-rockchip-armv8-friendlyarm_nanopi-r5c-squashfs-sysupgrade.img.gz
dist/hardware/nanopi-r5c/openwrt-24.10.5-rockchip-armv8-friendlyarm_nanopi-r5c-ext4-sysupgrade.img.gz
```

首次写入 SD 卡（假设卡是 `/dev/sdX`）：

```bash
gzip -dc dist/hardware/nanopi-r5c/openwrt-24.10.5-rockchip-armv8-friendlyarm_nanopi-r5c-squashfs-sysupgrade.img.gz \
  | sudo dd of=/dev/sdX bs=4M conv=fsync status=progress
```

从已有 OpenWrt 升级：

```bash
sysupgrade -v /path/to/openwrt-24.10.5-rockchip-armv8-friendlyarm_nanopi-r5c-squashfs-sysupgrade.img.gz
```

写入 eMMC 时请使用 FriendlyARM 对应 R5C 版本的 eMMC 安装流程（通常先从 SD 启动，再按厂商说明写入 eMMC）。不要把 `sdX` 写错。

## WankeCloud / S805

官方 OpenWrt `downloads.openwrt.org` 没有 `s805` 的 Image Builder，因此默认矩阵会明确报错：

```text
Image Builder download failed for WankeCloud
Check target/version or set imagebuilder_url in devices.yaml.
```

解决办法是使用社区或自行构建的 S805 Image Builder，并把完整 tarball 地址填入：

```yaml
- name: "WankeCloud"
  target: "s805"
  profile: "Thunder One Cloud"
  arch: "armv7"
  post_script: "amlogic-pack.sh"
  packages: "-luci -luci-ssl"
  imagebuilder_url: "https://<community-or-local-mirror>/openwrt-imagebuilder-s805...tar.xz"
```

如果没有公开链接，需要从 OpenWrt 源码构建 S805 Image Builder，或使用社区维护的 WankeCloud 固件构建仓库。脚本不会伪造来源或下载地址。

## Amlogic 打包

`amlogic-pack.sh` 需要以下环境变量：

```bash
export AML_BOOTLOADER=/path/to/u-boot.bin
export AML_DTB=/path/to/wankecloud.dtb
export AML_AMLIMG=/path/to/AmlImg
```

脚本会检查三个文件/工具是否存在，并给出错误提示。玩客云最终 `.burn.img` 还需要板级分区模板；不同批次的 WankeCloud 可能使用不同 DDR/分区布局，必须使用对应硬件的 bootloader 和 DTB，不能混用。
