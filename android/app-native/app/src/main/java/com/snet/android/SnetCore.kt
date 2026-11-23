package com.snet.android

import com.snet.android.snetbind.Snetbind

/**
 * Thin Kotlin facade over the gomobile-generated Snetbind class.
 *
 * Keep all bindings in this file so the generated Java API only has to be
 * adapted in one place.
 */
object SnetCore {
    fun start(configDir: String, tunFD: Int, ctlPort: Int): String =
        Snetbind.start(configDir, tunFD, ctlPort)

    fun stop(): String = Snetbind.stop()

    fun clearTun(): String = Snetbind.clearAndroidTun()

    fun getStatus(): String = Snetbind.getStatus()

    fun setLocalIPv6(addr: String) = Snetbind.setLocalIPv6(addr)
}
