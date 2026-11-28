package com.afret0.phonemic

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.net.InetSocketAddress
import java.net.Socket
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException

internal val usbServer = Srv("USB 直连", "127.0.0.1", 8443, 8080)

internal fun shouldAutoSelectUsb(selected: Srv?, usb: Srv?): Boolean = usb != null && selected == null

internal fun shouldClearUsb(selected: Srv?, usbAvailable: Boolean): Boolean =
    !usbAvailable && selected != null && selected.host == usbServer.host && selected.port == usbServer.port

suspend fun probeUsbServer(ctx: Context): Srv? = withContext(Dispatchers.IO) {
    val reachable = runCatching {
        Socket().use { it.connect(InetSocketAddress(usbServer.host, usbServer.port), 800) }
        true
    }.getOrDefault(false)
    if (!reachable) return@withContext null

    val url = "https://${usbServer.host}:${usbServer.port}/status"
    val pinned = CaStore.client(ctx, usbServer.host).newBuilder().callTimeout(4, TimeUnit.SECONDS).build()
    val first = runCatching { pinned.newCall(Request.Builder().url(url).build()).execute().use { it.isSuccessful } }
    if (first.getOrDefault(false)) return@withContext usbServer
    val exc = first.exceptionOrNull()
    if (CaStore.storedCa(ctx, usbServer.host) != null && exc !is SSLException) return@withContext null
    val trusted = runCatching { CaStore.ensureTrusted(ctx, usbServer) }.getOrNull() ?: return@withContext null
    runCatching {
        trusted.newBuilder().callTimeout(4, TimeUnit.SECONDS).build()
            .newCall(Request.Builder().url(url).build()).execute().use { if (it.isSuccessful) usbServer else null }
    }.getOrNull()
}