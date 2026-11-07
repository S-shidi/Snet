# Android 客户端（Compose 原生）

当前 Android 客户端为 **Jetpack Compose 原生实现**（`android/app-native/`），不再使用 WebView + `@JavascriptInterface` 的 Web 适配方式。本文档为当前实现的事实参考；历史规划与一次性报告已归档（见 `docs/archive/` 与 `deploy/archive/`）。

> 变更时间线：v0.11.x 期间从 WebView 架构重构为 Compose 原生。旧 WebView 工程
> （`android/app/` + `android/src/android.ts` + `shared/web` Android 产物）已删除。

## 技术栈

- Kotlin + Jetpack Compose（Material 3）
- gomobile AAR（`snetbind` → `libgojni.so`）桥接 Go daemon
- Gradle（`android/gradlew`，模块 `:app-native`）

## 目录结构

```
android/
├── app-native/                    # 主模块
│   ├── build.gradle
│   ├── libs/                      # gomobile AAR（classes.jar + jniLibs）
│   ├── src/main/
│       ├── AndroidManifest.xml
│       └── kotlin/com/snet/app/
│           ├── MainActivity.kt    # Compose 宿主、权限请求、QR 扫描
│           ├── SnetApp.kt         # Application（初始化 SnetBridge）
│           ├── SnetBridge.kt      # gomobile 绑定层（所有 Go 调用入口）
│           ├── SnetVpnService.kt  # VPN Service（utun fd、动态路由、热重启）
│           ├── RoutePlanner.kt    # 网络 CIDR + peer 子网、LAN/server 排除
│           ├── HardwareID.kt      # 设备 ID 派生（SHA-256）
│           ├── BootReceiver.kt    # BOOT_COMPLETED 开机自启
│           ├── repository/
│           │   └── SnetRepository.kt   # 挂起函数 + Dispatchers.IO 封装
│           ├── ui/
│           │   ├── MainScreen.kt  # 标签页（网络/状态）+ 列表 + 卡片
│           │   ├── NetworkDetailScreen.kt
│           │   ├── NetworkDialogs.kt   # 创建/加入/绑定/成员/设置等对话框
│           │   ├── Components.kt       # 通用卡片/状态/二维码/确认框
│           │   └── SettingsScreen.kt
│           └── viewmodel/
│               └── MainViewModel.kt
│   └── src/test/kotlin/com/snet/app/RoutePlannerTest.kt
├── build-android.sh               # 一键构建（gomobile bind + gradlew）
├── build.gradle / settings.gradle / gradlew
```

## 构建

```bash
./android/build-android.sh                 # 默认 android/arm64 + assembleDebug
TARGET=android ./android/build-android.sh  # 全 ABI
ASSEMBLE=assembleRelease ./android/build-android.sh  # 需签名环境变量
# 1) gomobile bind → snet.aar
# 2) 解包 classes.jar 到 app-native/libs, jni/* 到 app-native/jniLibs
# 3) ./gradlew $ASSEMBLE
# 产物: android/app-native/build/outputs/apk/{debug,release}/*.apk
```

脚本为 POSIX `sh`，可用 `JAVA_HOME` / `ANDROID_HOME` / `ANDROID_NDK_HOME` / `TARGET` /
`ASSEMBLE` 覆盖默认路径。版本号由 `VERSION` 和 `scripts/bump-version.sh` 维护；
`versionCode` 单调递增。

Release 签名从环境变量读取：

```bash
export SNET_ANDROID_KEYSTORE=/path/to/release.keystore
export SNET_ANDROID_KEYSTORE_PASSWORD=...
export SNET_ANDROID_KEY_ALIAS=...
export SNET_ANDROID_KEY_PASSWORD=...
ASSEMBLE=assembleRelease ./android/build-android.sh
```

CI（`.github/workflows/build-android.yml`）在 `main` 推送时构建 debug APK；在 `v*` tag
（由 `.github/workflows/release.yml` 调用）时先 `gomobile bind`，再构建并校验签名的
release APK。CI Secrets 见 `docs/RELEASE.md`。

## 已实现功能

网络标签页 + 状态标签页；创建/加入/退出/删除网络；邀请二维码生成与扫码加入；
设备授权绑定/续期；auth 过期与 pending join 横幅；成员审批、角色调整、移除成员；
网络设置（描述/标签/可见性/审批）；子网检测与更新；NAT 映射开关；VPN 服务与开机自启。
绑定/加入对话框在提交期间显示原地进度，失败时在对话框内展示错误；扫码固定竖屏，
识别成功后自动提交加入；加入 active 网络后自动启动 VPN 或请求 VPN 权限。

## 关键设计

- **桥接**：`SnetBridge.kt` 通过 gomobile `SnetCore`（`snetbind/snetcore.go`）调用 Go；
  `SnetRepository` 用 `withContext(Dispatchers.IO)` 把阻塞调用移出主线程，UI 经 `StateFlow`
  更新。无 `@JavascriptInterface`、无 `WebBridge`。
- **VPN**：`SnetVpnService` 建立 utun（MTU 1320），路由为已加入网络 CIDR + peer `allowedSubnets`（不再固定 `10.0.0.0/8`）；
  排除协调服务器 IP 与本机 LAN，禁止 `0.0.0.0/0`。本机 LAN 位于 `10.x` 时，
  `RoutePlanner` 允许更具体的 `excludeRoute`（API 33+，如 `10.7.86.0/24`），但拒绝
  与网络 CIDR/peer 子网重叠或包含 overlay IP 的排除；路由变化时
  `HaltTunnels()` → 重建 TUN → `onTunFdReady()`。
- **设备 ID**：`Build.MANUFACTURER + Build.MODEL + ANDROID_ID → SHA-256 → 16 hex`（硬件派生）。
- **状态刷新**：前台 `RESUMED` 时 5s 轮询 `status()`；授权状态每 5 分钟复查。
- **绑定/加入反馈**：`MainViewModel` 分别暴露 `bindLoading/bindError` 与
  `joinLoading/joinError`；对话框内联显示进度和错误，不依赖会被遮住的 Snackbar。
- **扫码**：`CaptureActivity` 固定 portrait，成功后直接调用 join 并回填链接。
- **入网启动 VPN**：join 返回 active 后调用 `ensureVpnForNetwork()`；无权限时进入
  `needVpnPermission` 流程，拒绝则断开网络并清 `auto_connect`。启动期的
  `VPN TUN 未就绪` 不再作为卡片错误展示。
- **授权期限**：Go 对永久授权返回 `authExpiresAt: null`；Android 解析时统一转成
  Kotlin `null`，已绑定且无到期时间显示“永久”。状态页已绑定显示授权期限；
  未绑定不显示日期，改为“去绑定设备”按钮和引导文案。

## 服务端 / 授权联动

- 服务器固定地址 `https://snet.uizhi.eu.org:8090`（`internal/constants`），客户端
  create/join/bind 不再提供服务器地址输入；桌面/Android 在「设置」里用授权码绑定。
- `-require-device-auth` 开启后，未绑定设备无法 create/join/register。

## 相关文档

- 深度旧文档（功能对齐计划、优化计划/报告、重构方案、UI 对齐方案、测试指南/报告、
  WebView 时代产物）→ `docs/archive/` 保留备查。
- 部署与 NAS 更新一次性记录 → `deploy/archive/`。