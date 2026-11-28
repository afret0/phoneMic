package com.afret0.phonemic

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

private const val TAG = "MicService"
private const val CHANNEL_ID = "phonemic_mic"
private const val NOTIF_ID = 1

class MicService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            "start" -> {
                val host = intent.getStringExtra("host") ?: run { stopSelf(); return START_NOT_STICKY }
                val port = intent.getIntExtra("port", 8443)
                val httpPort = intent.getIntExtra("httpPort", 8080)
                val name = intent.getStringExtra("name") ?: host
                val srv = Srv(name, host, port, httpPort)
                val mode = parseMode(intent.getStringExtra("mode") ?: "mic")
                MicState.mode.value = mode
                val fgsType = if (mode == StreamMode.Speaker) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK else ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
                ensureChannel()
                ServiceCompat.startForeground(
                    this, NOTIF_ID, buildNotification("正在连接电脑…"),
                    if (Build.VERSION.SDK_INT >= 29) fgsType else 0,
                )
                val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
                wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "phonemic:mic").apply { acquire(4 * 60 * 60 * 1000L) }
                val wm = getSystemService(Context.WIFI_SERVICE) as WifiManager
                wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "phonemic:mic").apply {
                    setReferenceCounted(false)
                    acquire()
                }
                Log.i(TAG, "recv, 前台服务启动, host: $host, port: $port, mode: $mode")
                Log.i(TAG, "hit, WiFi 高性能锁已持有, 防省电模式引入延迟抖动")
                scope.launch {
                    while (isActive) {
                        delay(3 * 60 * 60 * 1000L)
                        wakeLock?.acquire(4 * 60 * 60 * 1000L)
                        Log.i(TAG, "hit, CPU 锁续期, timeoutMs: ${4 * 60 * 60 * 1000L}")
                    }
                }
                scope.launch { MicState.phase.collect { updateNotification(phaseText(it)) } }
                scope.launch {
                    if (mode == StreamMode.Speaker) SpeakerEngine(this@MicService).run(srv) else MicEngine(this@MicService).run(srv)
                    stopSelf()
                }
            }
            "stop" -> {
                Log.i(TAG, "recv, 收到停止指令")
                MicState.phase.value = Phase.Idle
                MicState.level.value = 0f
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
            else -> stopSelf()
        }
        return START_NOT_STICKY
    }

    private fun phaseText(p: Phase): String = when (p) {
        Phase.Idle -> "已停止"
        Phase.Connecting -> "正在连接电脑…"
        Phase.Streaming -> if (MicState.mode.value == StreamMode.Speaker) "正在收听电脑声音 · 点击打开" else "正在推流 · 点击打开"
        is Phase.Reconnecting -> "重连中(第${p.attempt}次) · ${p.reason}"
        is Phase.Failed -> p.msg
    }

    private fun ensureChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL_ID) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL_ID, "麦克风推流", NotificationManager.IMPORTANCE_LOW))
        }
    }

    private fun buildNotification(text: String): Notification {
        val pi = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val stopPi = PendingIntent.getService(
            this, 1, Intent(this, MicService::class.java).setAction("stop"),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("phoneMic")
            .setContentText(text)
            .setContentIntent(pi)
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, "停止", stopPi).build())
            .build()
    }

    private fun updateNotification(text: String) {
        getSystemService(NotificationManager::class.java).notify(NOTIF_ID, buildNotification(text))
    }

    override fun onDestroy() {
        scope.cancel()
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
        wifiLock?.let { if (it.isHeld) it.release() }
        wifiLock = null
        Log.i(TAG, "done, 前台服务已销毁")
        super.onDestroy()
    }
}

fun startMic(ctx: Context, srv: Srv) {
    val i = Intent(ctx, MicService::class.java).setAction("start")
        .putExtra("host", srv.host).putExtra("port", srv.port)
        .putExtra("httpPort", srv.httpPort).putExtra("name", srv.name)
        .putExtra("mode", if (MicState.mode.value == StreamMode.Speaker) "speaker" else "mic")
    ContextCompat.startForegroundService(ctx, i)
}

fun stopMic(ctx: Context) {
    ContextCompat.startForegroundService(ctx, Intent(ctx, MicService::class.java).setAction("stop"))
}