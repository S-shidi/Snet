package com.snet.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat

/**
 * Owns the Android TUN interface and hands its fd to the gomobile daemon.
 *
 * P0 uses a fixed address/route inside the Snet auto-allocation pool. A later
 * phase can update the VpnService.Builder from the network returned by
 * /ctl/status and rebuild the interface on network changes.
 */
class SnetVpnService : VpnService() {

    private var tun: ParcelFileDescriptor? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (tun != null) {
            return START_STICKY
        }

        startForeground(NOTIFICATION_ID, buildNotification())

        val builder = Builder()
            .setSession("Snet")
            .setMtu(1320)
            .addAddress("10.88.0.2", 32)
            .addRoute("10.80.0.0", 12)
            .setBlocking(false)

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
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentTitle(getString(R.string.vpn_notification_title))
            .setContentText(getString(R.string.vpn_notification_text))
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
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

        fun start(context: Context) {
            ContextCompat.startForegroundService(
                context,
                Intent(context, SnetVpnService::class.java)
            )
        }
    }
}
