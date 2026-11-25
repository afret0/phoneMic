package com.afret0.phonemic

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.util.Log
import java.util.concurrent.ConcurrentLinkedQueue

private const val TAG = "Discovery"
private const val SERVICE_TYPE = "_phonemic._tcp."

internal fun shouldFollowSelected(sel: Srv?, srv: Srv): Boolean =
    sel != null && sel.name == srv.name && sel != srv

class Discovery(private val ctx: Context) {
    private val nsd = ctx.getSystemService(Context.NSD_SERVICE) as NsdManager
    private val wifi = ctx.getSystemService(Context.WIFI_SERVICE) as WifiManager
    private var multicastLock: WifiManager.MulticastLock? = null
    private var listener: NsdManager.DiscoveryListener? = null
    private val resolveQueue = ConcurrentLinkedQueue<NsdServiceInfo>()
    private var resolving = false

    fun start() {
        if (listener != null) return
        multicastLock = wifi.createMulticastLock("phonemic_mdns").apply {
            setReferenceCounted(false)
            acquire()
        }
        val l = object : NsdManager.DiscoveryListener {
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.e(TAG, "fail, mDNS 发现启动失败, code: $errorCode")
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.e(TAG, "fail, mDNS 发现停止失败, code: $errorCode")
            }

            override fun onDiscoveryStarted(serviceType: String) {
                Log.i(TAG, "done, mDNS 发现已启动, type: $serviceType")
            }

            override fun onDiscoveryStopped(serviceType: String) {
                Log.i(TAG, "done, mDNS 发现已停止")
            }

            override fun onServiceFound(info: NsdServiceInfo) {
                Log.i(TAG, "hit, 发现服务, name: ${info.serviceName}")
                resolveQueue.add(info)
                pumpResolve()
            }

            override fun onServiceLost(info: NsdServiceInfo) {
                Log.i(TAG, "skip, 服务下线, name: ${info.serviceName}")
                MicState.servers.value = MicState.servers.value.filterNot { it.name == info.serviceName }
            }
        }
        listener = l
        runCatching { nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, l) }
            .onFailure { Log.e(TAG, "fail, mDNS 发现异常, err: ${it.message}") }
    }

    @Synchronized
    private fun pumpResolve() {
        if (resolving) return
        val next = resolveQueue.poll() ?: return
        resolving = true
        runCatching {
            nsd.resolveService(next, object : NsdManager.ResolveListener {
                override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) {
                    Log.e(TAG, "fail, 解析失败, name: ${info.serviceName}, code: $errorCode")
                    synchronized(this@Discovery) {
                        resolving = false
                        pumpResolve()
                    }
                }

                override fun onServiceResolved(info: NsdServiceInfo) {
                    val host = info.host?.hostAddress
                    if (host == null) {
                        Log.i(TAG, "skip, 解析结果无地址, name: ${info.serviceName}")
                    } else {
                        val httpPort = info.attributes["http"]?.decodeToString()?.toIntOrNull() ?: 8080
                        val srv = Srv(info.serviceName, host, info.port, httpPort)
                        MicState.servers.value = (MicState.servers.value.filterNot { it.name == srv.name } + srv)
                        val sel = MicState.selected.value
                        if (shouldFollowSelected(sel, srv)) {
                            Log.i(TAG, "hit, 同名电脑地址变化, 选中项自动跟随, name: ${srv.name}, ${sel!!.host}:${sel.port} -> ${srv.host}:${srv.port}")
                            MicState.selected.value = srv
                            persistSelected(ctx, srv)
                        }
                        Log.i(TAG, "done, 解析成功, name: ${srv.name}, host: ${srv.host}, port: ${srv.port}, httpPort: ${srv.httpPort}")
                    }
                    synchronized(this@Discovery) {
                        resolving = false
                        pumpResolve()
                    }
                }
            })
        }.onFailure {
            resolving = false
            Log.e(TAG, "fail, 解析异常, err: ${it.message}")
        }
    }

    fun stop() {
        listener?.let { runCatching { nsd.stopServiceDiscovery(it) } }
        listener = null
        resolveQueue.clear()
        multicastLock?.let { if (it.isHeld) it.release() }
        multicastLock = null
    }

    fun refresh(clear: Boolean) {
        Log.i(TAG, "recv, 刷新局域网发现, clear: $clear, 此前运行中: ${listener != null}")
        if (clear) MicState.servers.value = emptyList()
        stop()
        start()
    }
}