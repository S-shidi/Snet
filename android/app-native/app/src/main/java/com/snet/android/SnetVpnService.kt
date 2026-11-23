package com.snet.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.system.OsConstants
import android.util.Log
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import org.json.JSONObject
import java.io.File
import java.net.Inet6Address

/**
 * Owns the Android TUN interface and hands its fd to the gomobile daemon.
 *
 * Network addresses/routes are read from the daemon config immediately before
 * establish(). When the WebView reports a membership change, the service is
 * restarted with the new routes.
 */
class SnetVpnService : VpnService() {

    private var tun: ParcelFileDescriptor? = null
    private val watchdog = Handler(Looper.getMainLooper())
    private var inactiveChecks = 0
    private var v6Ticks = 0
    private val watchdogRunnable = object : Runnable {
        override fun run() {
            if (!isRunning) return
            if (hasActiveNetwork(this@SnetVpnService)) {
                inactiveChecks = 0
            } else {
                inactiveChecks++
                // Two consecutive misses tolerate a config write in progress.
                if (inactiveChecks >= 2) {
                    Log.i(TAG, "no active network; stopping VPN")
                    stopVpn()
                    stopSelf()
                    return
                }
            }
            v6Ticks++
            if (v6Ticks >= 5) {
                v6Ticks = 0
                updateLocalIPv6()
            }
            watchdog.postDelayed(this, 2000)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_DISCONNECT -> {
                stopVpn()
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_RESTART -> {
                if (!hasActiveNetwork(this)) {
                    stopVpn()
                    stopSelf()
                    return START_NOT_STICKY
                }
                return rebuildVpn()
            }
        }
        if (!hasActiveNetwork(this)) {
            stopVpn()
            stopSelf()
            return START_NOT_STICKY
        }
        if (tun != null) {
            return START_STICKY
        }
        return startVpn()
    }

    private fun startVpn(): Int {
        startForeground(NOTIFICATION_ID, buildNotification())

        val pfd = establishTunnel()
        if (pfd == null) {
            toast("创建 TUN 失败")
            stopSelf()
            return START_NOT_STICKY
        }
        tun = pfd

        val err = SnetCore.start(filesDir.absolutePath, pfd.fd, CTL_PORT)
        if (err.isNotEmpty()) {
            Log.e(TAG, "SnetCore.start: $err")
            toast("启动失败: $err")
            SnetCore.clearTun()
            pfd.close()
            tun = null
            stopSelf()
            return START_NOT_STICKY
        }

        isRunning = true
        inactiveChecks = 0
        v6Ticks = 0
        updateLocalIPv6()
        watchdog.removeCallbacks(watchdogRunnable)
        watchdog.postDelayed(watchdogRunnable, 2000)
        Log.i(TAG, "snetd started with TUN fd ${pfd.fd}")
        return START_STICKY
    }

    /**
     * Rebuild the TUN after network membership/routes changed, without
     * stopping snetd or its ctl proxy. The daemon swaps its duplicated fd and
     * rebuilds tunnels; an in-flight /ctl/create or /ctl/join request is not
     * interrupted.
     */
    private fun rebuildVpn(): Int {
        val old = tun ?: return startVpn()
        startForeground(NOTIFICATION_ID, buildNotification())

        val pfd = establishTunnel()
        if (pfd == null) {
            Log.e(TAG, "rebuild TUN failed; keeping previous interface")
            return START_STICKY
        }
        tun = pfd

        val err = SnetCore.start(filesDir.absolutePath, pfd.fd, CTL_PORT)
        if (err.isNotEmpty()) {
            Log.e(TAG, "rebuild SnetCore.start: $err")
            pfd.close()
            tun = old
            return START_STICKY
        }

        old.close()
        isRunning = true
        inactiveChecks = 0
        v6Ticks = 0
        updateLocalIPv6()
        watchdog.removeCallbacks(watchdogRunnable)
        watchdog.postDelayed(watchdogRunnable, 2000)
        Log.i(TAG, "TUN rebuilt with fd ${pfd.fd}")
        return START_STICKY
    }

    private fun establishTunnel(): ParcelFileDescriptor? {
        val builder = Builder()
            .setSession("Snet")
            .setMtu(1320)
            .setBlocking(false)
            // The overlay is IPv4-only. Without allowFamily, Android blocks
            // IPv6 for apps inside an IPv4-only VPN, so the carrier's IPv6
            // address becomes unreachable and IPv6 direct dials always fail.
            .allowFamily(OsConstants.AF_INET6)
            .addDnsServer("1.1.1.1")
            .addDnsServer("8.8.8.8")

        addConfiguredNetworks(builder)

        // Keep snetd's own transport sockets (relay links, direct candidates,
        // probes) on the physical network. The ctl API is loopback-only, so
        // excluding the app from the VPN does not affect the UI or the tunnel.
        try {
            builder.addDisallowedApplication(packageName)
        } catch (e: Exception) {
            Log.w(TAG, "cannot exclude app from VPN", e)
        }

        return try {
            builder.establish()
        } catch (e: Exception) {
            Log.e(TAG, "establish TUN failed", e)
            null
        }
    }

    private fun addConfiguredNetworks(builder: Builder) {
        val addresses = linkedSetOf<String>()
        val routes = linkedSetOf<Pair<String, Int>>()
        val config = File(filesDir, "config.json")

        if (config.exists()) {
            try {
                val networks = JSONObject(config.readText()).optJSONObject("networks")
                if (networks != null) {
                    val keys = networks.keys()
                    while (keys.hasNext()) {
                        val network = networks.optJSONObject(keys.next()) ?: continue
                        if (!network.optBoolean("active", true)) continue

                        val ip = network.optString("ip", "").trim()
                        if (ip.isNotEmpty()) addresses.add(ip)

                        val subnet = network.optString("subnet", "").trim()
                        if (subnet.isNotEmpty()) {
                            val parts = subnet.split("/", limit = 2)
                            val prefix = parts.getOrNull(1)?.toIntOrNull() ?: 24
                            if (parts[0].isNotEmpty() && prefix in 0..32) {
                                routes.add(parts[0] to prefix)
                            }
                        }
                    }
                }
            } catch (e: Exception) {
                Log.w(TAG, "cannot read network config", e)
            }
        }

        // Delegated peer routes are not persisted in config.json; read the
        // daemon status so Android also routes LANs advertised by peers (for
        // example 192.168.1.0/24 via the sdserver gateway).
        try {
            val status = JSONObject(SnetCore.getStatus())
            val networks = status.optJSONArray("networks")
            if (networks != null) {
                for (i in 0 until networks.length()) {
                    val network = networks.optJSONObject(i) ?: continue
                    if (!network.optBoolean("active", true)) continue
                    val subnets = network.optJSONArray("peerSubnets") ?: continue
                    for (j in 0 until subnets.length()) {
                        val cidr = subnets.optString(j, "").trim()
                        if (cidr.isEmpty()) continue
                        val parts = cidr.split("/", limit = 2)
                        val prefix = parts.getOrNull(1)?.toIntOrNull() ?: continue
                        if (parts[0].isNotEmpty() && prefix in 0..32) {
                            routes.add(parts[0] to prefix)
                        }
                    }
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "cannot read delegated routes from daemon status", e)
        }

        addresses.forEach { builder.addAddress(it, 32) }
        routes.forEach { (network, prefix) -> builder.addRoute(network, prefix) }
    }

    override fun onRevoke() {
        stopVpn()
        super.onRevoke()
    }

    override fun onDestroy() {
        stopVpn()
        super.onDestroy()
    }

    /**
     * The VPN is IPv4-only and Go's interface enumeration can miss the
     * carrier's global IPv6, so read it from the underlying network and hand it
     * to the daemon. Without this the node never advertises a v6 endpoint and
     * every IPv6 direct attempt is skipped.
     */
    private fun updateLocalIPv6() {
        try {
            val cm = getSystemService(ConnectivityManager::class.java) ?: return
            for (network in cm.allNetworks) {
                val caps = cm.getNetworkCapabilities(network) ?: continue
                if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
                if (!caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) continue
                val lp = cm.getLinkProperties(network) ?: continue
                for (link in lp.linkAddresses) {
                    val ip = link.address
                    if (ip !is Inet6Address) continue
                    if (ip.isLoopbackAddress || ip.isLinkLocalAddress) continue
                    val raw = ip.address
                    if (raw.isNotEmpty() && (raw[0].toInt() and 0xfe) == 0xfc) continue // ULA fc00::/7
                    val host = ip.hostAddress ?: continue
                    SnetCore.setLocalIPv6(host)
                    return
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "local IPv6 lookup failed", e)
        }
    }

    private fun stopVpn() {
        watchdog.removeCallbacks(watchdogRunnable)
        inactiveChecks = 0
        v6Ticks = 0
        SnetCore.setLocalIPv6("")
        val err = SnetCore.clearTun()
        if (err.isNotEmpty()) {
            Log.w(TAG, "SnetCore.stop: $err")
        }
        tun?.close()
        tun = null
        isRunning = false
        stopForeground(STOP_FOREGROUND_REMOVE)
    }

    private fun buildNotification(): Notification {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val manager = getSystemService(NotificationManager::class.java)
            val channel = NotificationChannel(
                CHANNEL_ID,
                getString(R.string.vpn_notification_channel),
                NotificationManager.IMPORTANCE_LOW
            )
            manager.createNotificationChannel(channel)
        }

        val disconnect = PendingIntent.getService(
            this,
            1,
            Intent(this, SnetVpnService::class.java).setAction(ACTION_DISCONNECT),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val open = PendingIntent.getActivity(
            this,
            2,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentTitle(getString(R.string.vpn_notification_title))
            .setContentText(getString(R.string.vpn_notification_text))
            .setContentIntent(open)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .addAction(android.R.drawable.ic_menu_close_clear_cancel, getString(R.string.disconnect), disconnect)
            .build()
    }

    private fun toast(message: String) {
        Toast.makeText(applicationContext, message, Toast.LENGTH_LONG).show()
    }

    companion object {
        private const val TAG = "SnetVpnService"
        private const val CHANNEL_ID = "snet_vpn"
        private const val NOTIFICATION_ID = 1001
        private const val CTL_PORT = 8081
        private const val ACTION_DISCONNECT = "com.snet.android.action.DISCONNECT"
        private const val ACTION_RESTART = "com.snet.android.action.RESTART"

        @Volatile
        var isRunning: Boolean = false
            private set

        fun start(context: Context) {
            // Never create a placeholder TUN on cold start or after the last
            // network was switched off. The status refresh starts the service
            // only when the daemon has an active network with a real address.
            if (isRunning || !hasActiveNetwork(context)) return
            ContextCompat.startForegroundService(
                context,
                Intent(context, SnetVpnService::class.java)
            )
        }

        private fun hasActiveNetwork(context: Context): Boolean {
            val config = File(context.filesDir, "config.json")
            if (!config.exists()) return false
            return try {
                val networks = JSONObject(config.readText()).optJSONObject("networks") ?: return false
                val keys = networks.keys()
                while (keys.hasNext()) {
                    val network = networks.optJSONObject(keys.next()) ?: continue
                    if (network.optBoolean("active", true) && network.optString("ip", "").isNotBlank()) {
                        return true
                    }
                }
                false
            } catch (_: Exception) {
                false
            }
        }

        fun restart(context: Context) {
            if (!isRunning) return
            ContextCompat.startForegroundService(
                context,
                Intent(context, SnetVpnService::class.java).setAction(ACTION_RESTART)
            )
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, SnetVpnService::class.java))
        }
    }
}
