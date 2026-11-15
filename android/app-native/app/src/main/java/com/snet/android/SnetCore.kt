package com.snet.android

/**
 * Thin Kotlin facade over the gomobile-generated Snetbind class.
 *
 * Keep all bindings in this file so the generated Java API only has to be
 * adapted in one place.
 */
object SnetCore {
    fun start(configDir: String, tunFD: Int, ctlPort: Int): String =
        Snetbind.Start(configDir, tunFD, ctlPort)

    fun stop(): String = Snetbind.Stop()

    fun getStatus(): String = Snetbind.GetStatus()
}
