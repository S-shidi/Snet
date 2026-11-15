package com.snet.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import org.json.JSONObject
import java.io.File

/**
 * Owns the Android TUN interface and hands its fd to the gomobile daemon.
 *
 * Network addresses/routes are read from the daemon config immediately before
 * establish(). When the WebView reports a membership change, the service is
 * restarted with the new routes.
 */
class SnetVpnService : VpnService() {

    private var tun: ParcelFileDescriptor? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_DISCONNECT -> {
                stopVpn()
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_RESTART -> {
                stopVpn()
                return startVpn()
            }
        }
        if (tun != null) {
            return START_STICKY
        }
        return startVpn()
    }

    private fun startVpn(): Int {
        startForeground(NOTIFICATION_ID, buildNotification())

        val builder = Builder()
            .setSession("Snet")
            .setMtu(1320)
            .setBlocking(false)
            .addDnsServer("1.1.1.1")
            .addDnsServer("8.8.8.8")

        addConfiguredNetworks(builder)

        val pfd = try {
            builder.establish()
        } catch (e: Exception) {
            Log.e(TAG, "establish TUN failed", e)
            null
        }
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
            SnetCore.stop()
            pfd.close()
            tun = null
            stopSelf()
            return START_NOT_STICKY
        }

        Log.i(TAG, "snetd started with TUN fd ${pfd.fd}")
        return START_STICKY
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

        // First run / no joined network: establish a harmless placeholder so
        // the WebView can join a network. The bridge restarts the service when
        // the config gains its real addresses and routes.
        if (addresses.isEmpty()) addresses.add("10.88.0.2")
        if (routes.isEmpty()) routes.add("10.80.0.0" to 12)

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

    private fun stopVpn() {
        val err = SnetCore.stop()
        if (err.isNotEmpty()) {
            Log.w(TAG, "SnetCore.stop: $err")
        }
        tun?.close()
        tun = null
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

        fun start(context: Context) {
            ContextCompat.startForegroundService(
                context,
                Intent(context, SnetVpnService::class.java)
            )
        }

        fun restart(context: Context) {
            ContextCompat.startForegroundService(
                context,
                Intent(context, SnetVpnService::class.java).setAction(ACTION_RESTART)
            )
        }
    }
}
