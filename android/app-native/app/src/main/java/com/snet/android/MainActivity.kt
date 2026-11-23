package com.snet.android

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.webkit.JavascriptInterface
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.FileProvider
import androidx.webkit.WebViewAssetLoader
import androidx.webkit.WebViewClientCompat
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.atomic.AtomicBoolean

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private val mainHandler = Handler(Looper.getMainLooper())
    private var vpnPermissionDenied = false
    private var notificationPermissionRequested = false
    private val apkDownloading = AtomicBoolean(false)
    @Volatile
    private var pendingInstallApk: File? = null
    private val restartVpnRunnable = Runnable {
        // A network switch-off may have stopped the service after this rebuild
        // was queued. Do not resurrect a placeholder TUN in that case.
        if (!SnetVpnService.isRunning) return@Runnable
        if (VpnService.prepare(this) == null) {
            SnetVpnService.restart(this)
        }
    }

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            vpnPermissionDenied = false
            startVpnService()
        } else {
            vpnPermissionDenied = true
            Toast.makeText(this, "未授予 VPN 权限", Toast.LENGTH_SHORT).show()
        }
    }

    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { /* A foreground VPN can still run; the notification is best-effort. */ }

    private val scanLauncher = registerForActivityResult(ScanContract()) { result ->
        val contents = result.contents
        if (contents.isNullOrBlank()) {
            sendScanError("扫码已取消")
        } else {
            sendScanResult(contents)
        }
    }

    private val installPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) {
        val apk = pendingInstallApk ?: return@registerForActivityResult
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O || packageManager.canRequestPackageInstalls()) {
            pendingInstallApk = null
            launchApkInstallIntent(apk)
        } else {
            sendUpgradeProgress("error", 0, 0, 0, "未授予「安装未知应用」权限", "请在系统设置中允许 Snet 安装未知应用后重试")
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        webView = findViewById(R.id.webview)
        configureWebView()
        // Load the local shell first. Starting the Go control plane can take a
        // few seconds on a cold device; waiting for it here left the WebView
        // black until SnetCore.start() returned. The shell retries its status
        // request and fills in as soon as the daemon is ready.
        webView.loadUrl("https://appassets.androidplatform.net/assets/index.html")
        // Cold start must not create a TUN or request VPN permission. It does
        // start the local control plane so create/join works before any VPN
        // exists; the status-bar key appears only after an explicit network
        // toggle/create/join.
        Thread {
            val err = SnetCore.start(filesDir.absolutePath, 0, 8081)
            if (err.isNotEmpty()) {
                runOnUiThread {
                    Toast.makeText(this, "本地服务启动失败: $err", Toast.LENGTH_LONG).show()
                }
            }
        }.start()
    }

    private fun setVpnActive(active: Boolean) {
        if (!active) {
            vpnPermissionDenied = false
            mainHandler.removeCallbacks(restartVpnRunnable)
            SnetVpnService.stop(this)
            return
        }
        // The WebView may call this on every status refresh. Once the service
        // is up, onNetworksChanged() owns route rebuilds; do not re-prompt for
        // VPN/notification permission or start the service again. After a
        // denial, wait for the user to toggle the network off/on instead of
        // re-opening the system VPN prompt on every poll.
        if (SnetVpnService.isRunning || vpnPermissionDenied) return
        requestNotificationPermissionIfNeeded()
        maybeRequestIgnoreBatteryOptimizations()
        requestVpnPermission()
    }

    private fun requestVpnPermission() {
        val intent = VpnService.prepare(this)
        if (intent != null) {
            vpnPermissionLauncher.launch(intent)
        } else {
            startVpnService()
        }
    }

    private fun startVpnService() {
        SnetVpnService.start(this)
    }

    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            if (notificationPermissionRequested) return
            notificationPermissionRequested = true
            notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    private fun maybeRequestIgnoreBatteryOptimizations() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return
        val pm = getSystemService(PowerManager::class.java)
        if (pm.isIgnoringBatteryOptimizations(packageName)) return

        val prefs = getSharedPreferences("snet", MODE_PRIVATE)
        if (prefs.getBoolean("battery_prompted", false)) return
        prefs.edit().putBoolean("battery_prompted", true).apply()

        AlertDialog.Builder(this)
            .setTitle(R.string.battery_title)
            .setMessage(R.string.battery_message)
            .setPositiveButton(R.string.battery_settings) { _, _ ->
                val request = Intent(
                    Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                    Uri.parse("package:$packageName")
                )
                try {
                    startActivity(request)
                } catch (_: Exception) {
                    startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                }
            }
            .setNegativeButton(R.string.later, null)
            .show()
    }

    private fun configureWebView() {
        val settings = webView.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        settings.loadWithOverviewMode = false
        settings.useWideViewPort = true
        webView.setBackgroundColor(Color.parseColor("#0D1017"))
        if (BuildConfig.DEBUG) {
            WebView.setWebContentsDebuggingEnabled(true)
        }
        webView.addJavascriptInterface(NetworkBridge(), "SnetAndroid")

        val assetLoader = WebViewAssetLoader.Builder()
            .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(this))
            .build()

        webView.webViewClient = object : WebViewClientCompat() {
            override fun shouldInterceptRequest(
                view: WebView,
                request: WebResourceRequest
            ): WebResourceResponse? = assetLoader.shouldInterceptRequest(request.url)
        }

    }

    private fun sendScanResult(text: String) {
        webView.evaluateJavascript("window.__snetScanResult(" + JSONObject.quote(text) + ")", null)
    }

    private fun sendScanError(message: String) {
        webView.evaluateJavascript("window.__snetScanError(" + JSONObject.quote(message) + ")", null)
    }

    private fun sendUpgradeProgress(
        state: String,
        percent: Int,
        downloaded: Long,
        total: Long,
        message: String,
        error: String = ""
    ) {
        val payload = JSONObject().apply {
            put("state", state)
            put("percent", percent)
            put("downloaded", downloaded)
            put("total", total)
            put("message", message)
            if (error.isNotEmpty()) {
                put("error", error)
            }
        }
        runOnUiThread {
            webView.evaluateJavascript(
                "window.__snetUpgradeProgress && window.__snetUpgradeProgress($payload)",
                null
            )
        }
    }

    private fun candidateDownloadUrls(rawUrl: String): List<String> {
        val u = rawUrl.trim()
        if (u.isEmpty()) return emptyList()
        val ghproxy = "https://ghproxy.net/"
        if (u.startsWith("https://github.com/") || u.startsWith("https://objects.githubusercontent.com/")) {
            return listOf(u, ghproxy + u)
        }
        if (u.startsWith(ghproxy)) {
            val orig = u.removePrefix(ghproxy)
            if (orig.isNotEmpty()) return listOf(orig, u)
        }
        return listOf(u)
    }

    private fun downloadApkFromUrl(urlStr: String, destFile: File) {
        var currentUrl = urlStr
        var redirects = 0
        var conn: HttpURLConnection? = null
        try {
            while (redirects < 6) {
                val u = URL(currentUrl)
                conn = (u.openConnection() as HttpURLConnection).apply {
                    connectTimeout = 15_000
                    readTimeout = 30_000
                    instanceFollowRedirects = false
                    setRequestProperty("User-Agent", "Snet-Android-Updater")
                }
                val code = conn.responseCode
                if (code in listOf(
                        HttpURLConnection.HTTP_MOVED_PERM,
                        HttpURLConnection.HTTP_MOVED_TEMP,
                        HttpURLConnection.HTTP_SEE_OTHER,
                        307,
                        308
                    )
                ) {
                    val loc = conn.getHeaderField("Location") ?: throw Exception("重定向缺少 Location")
                    conn.disconnect()
                    currentUrl = URL(u, loc).toString()
                    redirects++
                    continue
                }
                if (code != HttpURLConnection.HTTP_OK) {
                    throw Exception("HTTP $code")
                }
                val total = conn.contentLengthLong
                val partFile = File(destFile.parentFile, "${destFile.name}.part")
                sendUpgradeProgress("downloading", 0, 0L, if (total > 0) total else 0L, "正在下载 APK 安装包…")

                val header = ByteArray(4)
                var headerFilled = 0
                var downloaded = 0L
                var lastReportAt = 0L

                conn.inputStream.use { input ->
                    FileOutputStream(partFile).use { output ->
                        val buf = ByteArray(32 * 1024)
                        while (true) {
                            val n = input.read(buf)
                            if (n < 0) break
                            if (n == 0) continue
                            if (headerFilled < 4) {
                                val take = minOf(4 - headerFilled, n)
                                System.arraycopy(buf, 0, header, headerFilled, take)
                                headerFilled += take
                            }
                            output.write(buf, 0, n)
                            downloaded += n
                            val now = System.currentTimeMillis()
                            if (now - lastReportAt >= 80) {
                                lastReportAt = now
                                val pct = if (total > 0) ((downloaded * 95L) / total).toInt().coerceIn(0, 95) else 50
                                sendUpgradeProgress("downloading", pct, downloaded, if (total > 0) total else 0L, "正在下载 APK 安装包…")
                            }
                        }
                        output.flush()
                    }
                }
                if (downloaded < 64 * 1024) {
                    partFile.delete()
                    throw Exception("下载的 APK 文件大小异常 ($downloaded 字节)")
                }
                if (total > 0 && downloaded != total) {
                    partFile.delete()
                    throw Exception("下载未完成 ($downloaded / $total 字节)")
                }
                if (headerFilled < 4 ||
                    header[0] != 'P'.code.toByte() ||
                    header[1] != 'K'.code.toByte() ||
                    header[2] != 0x03.toByte() ||
                    header[3] != 0x04.toByte()
                ) {
                    partFile.delete()
                    throw Exception("下载的文件不是有效的 APK 安装包")
                }
                if (destFile.exists()) destFile.delete()
                if (!partFile.renameTo(destFile)) {
                    partFile.copyTo(destFile, overwrite = true)
                    partFile.delete()
                }
                return
            }
            throw Exception("重定向次数过多")
        } finally {
            conn?.disconnect()
        }
    }

    private fun promptOrInstallApk(apkFile: File) {
        runOnUiThread {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !packageManager.canRequestPackageInstalls()) {
                pendingInstallApk = apkFile
                sendUpgradeProgress("installing", 98, apkFile.length(), apkFile.length(), "请授予「安装未知应用」权限以继续安装…")
                try {
                    val intent = Intent(
                        Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                        Uri.parse("package:$packageName")
                    )
                    installPermissionLauncher.launch(intent)
                } catch (e: Exception) {
                    sendUpgradeProgress("error", 0, 0, 0, "无法打开安装权限设置: ${e.message}", e.message ?: "权限申请失败")
                }
                return@runOnUiThread
            }
            launchApkInstallIntent(apkFile)
        }
    }

    private fun launchApkInstallIntent(apkFile: File) {
        try {
            val uri = FileProvider.getUriForFile(
                this@MainActivity,
                "$packageName.fileprovider",
                apkFile
            )
            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            sendUpgradeProgress("restarting", 100, apkFile.length(), apkFile.length(), "下载完成，正在拉起系统安装界面…")
            startActivity(intent)
        } catch (e: Exception) {
            sendUpgradeProgress("error", 0, 0, 0, "拉起系统安装器失败: ${e.message}", e.message ?: "拉起安装器失败")
        }
    }

    inner class NetworkBridge {
        @JavascriptInterface
        fun setVpnActive(active: Boolean) {
            runOnUiThread { this@MainActivity.setVpnActive(active) }
        }

        @JavascriptInterface
        fun onNetworksChanged() {
            runOnUiThread {
                // Route refresh only. If the user has not started the VPN,
                // this must not create a TUN or show the status-bar key.
                if (!SnetVpnService.isRunning) return@runOnUiThread
                // Give the create/join HTTP response a moment to reach the
                // WebView before rebuilding the TUN; the daemon itself keeps
                // running, so this is only a route/address refresh.
                mainHandler.removeCallbacks(restartVpnRunnable)
                mainHandler.postDelayed(restartVpnRunnable, 600)
            }
        }

        @JavascriptInterface
        fun saveDiagnosticLog(text: String): Boolean {
            return try {
                val file = File(cacheDir, "snet-diagnostic.log")
                file.writeText(text)
                val uri = FileProvider.getUriForFile(
                    this@MainActivity,
                    "$packageName.fileprovider",
                    file,
                )
                runOnUiThread {
                    val share = Intent(Intent.ACTION_SEND).apply {
                        type = "text/plain"
                        putExtra(Intent.EXTRA_STREAM, uri)
                        putExtra(Intent.EXTRA_SUBJECT, "Snet diagnostic log")
                        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    }
                    startActivity(Intent.createChooser(share, "导出诊断日志"))
                }
                true
            } catch (e: Exception) {
                runOnUiThread {
                    Toast.makeText(
                        this@MainActivity,
                        "导出失败: ${e.message}",
                        Toast.LENGTH_LONG,
                    ).show()
                }
                false
            }
        }

        @JavascriptInterface
        fun startScan() {
            runOnUiThread {
                val options = ScanOptions()
                    .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                    .setPrompt("扫描 Snet 邀请二维码")
                    .setBeepEnabled(false)
                    .setOrientationLocked(false)
                scanLauncher.launch(options)
            }
        }

        @JavascriptInterface
        fun downloadAndInstallApk(url: String) {
            val trimmed = url.trim()
            if (trimmed.isEmpty()) {
                sendUpgradeProgress("error", 0, 0, 0, "APK 下载地址为空", "APK 下载地址为空")
                return
            }
            if (!apkDownloading.compareAndSet(false, true)) {
                return
            }
            Thread {
                try {
                    val destFile = File(cacheDir, "snet-upgrade.apk")
                    val candidates = candidateDownloadUrls(trimmed)
                    var lastErr: Exception? = null
                    var ok = false
                    for (cand in candidates) {
                        try {
                            downloadApkFromUrl(cand, destFile)
                            ok = true
                            break
                        } catch (e: Exception) {
                            lastErr = e
                        }
                    }
                    if (!ok) {
                        val msg = lastErr?.message ?: "网络请求失败"
                        sendUpgradeProgress("error", 0, 0, 0, "下载 APK 失败: $msg", msg)
                        return@Thread
                    }
                    sendUpgradeProgress("installing", 98, destFile.length(), destFile.length(), "校验通过，正在准备安装…")
                    promptOrInstallApk(destFile)
                } finally {
                    apkDownloading.set(false)
                }
            }.start()
        }
    }

    override fun onResume() {
        super.onResume()
        // The user may have granted VPN access from system settings after the
        // permission dialog was dismissed. Let the next status refresh retry.
        if (vpnPermissionDenied && VpnService.prepare(this) == null) {
            vpnPermissionDenied = false
        }
        val apk = pendingInstallApk
        if (apk != null && (Build.VERSION.SDK_INT < Build.VERSION_CODES.O || packageManager.canRequestPackageInstalls())) {
            pendingInstallApk = null
            launchApkInstallIntent(apk)
        }
    }

    override fun onDestroy() {
        mainHandler.removeCallbacks(restartVpnRunnable)
        webView.destroy()
        super.onDestroy()
    }
}
