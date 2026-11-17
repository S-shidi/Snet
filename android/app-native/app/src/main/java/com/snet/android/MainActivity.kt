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
import androidx.webkit.WebViewAssetLoader
import androidx.webkit.WebViewClientCompat
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import org.json.JSONObject

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private val mainHandler = Handler(Looper.getMainLooper())
    private var vpnPermissionDenied = false
    private var notificationPermissionRequested = false
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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        webView = findViewById(R.id.webview)
        configureWebView()
        // Cold start must not create a TUN or request VPN permission. It does
        // start the local control plane so create/join works before any VPN
        // exists; the status-bar key appears only after an explicit network
        // toggle/create/join.
        Thread {
            val err = SnetCore.start(filesDir.absolutePath, 0, 8081)
            runOnUiThread {
                if (err.isNotEmpty()) {
                    Toast.makeText(this, "本地服务启动失败: $err", Toast.LENGTH_LONG).show()
                }
                webView.loadUrl("https://appassets.androidplatform.net/assets/index.html")
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
    }

    override fun onResume() {
        super.onResume()
        // The user may have granted VPN access from system settings after the
        // permission dialog was dismissed. Let the next status refresh retry.
        if (vpnPermissionDenied && VpnService.prepare(this) == null) {
            vpnPermissionDenied = false
        }
    }

    override fun onDestroy() {
        mainHandler.removeCallbacks(restartVpnRunnable)
        webView.destroy()
        super.onDestroy()
    }
}
