package com.afret0.phonemic

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.animateFloat
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentSize
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.math.log10

private val Bg = Color(0xFF0F0F23)
private val CardBg = Color(0xFF1B1B30)
private val BorderC = Color(0xFF312E81)
private val Indigo = Color(0xFF4338CA)
private val IndigoBright = Color(0xFF6D5BD8)
private val Green = Color(0xFF22C55E)
private val GreenDeep = Color(0xFF16A34A)
private val Fg = Color(0xFFF8FAFC)
private val Muted = Color(0xFF94A3B8)
private val Danger = Color(0xFFEF4444)
private val Amber = Color(0xFFF59E0B)

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { App() }
    }
}

private fun prefs(ctx: Context) = ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE)

internal fun persistSelected(ctx: Context, srv: Srv) {
    prefs(ctx).edit().putString("last_host", srv.host).putInt("last_port", srv.port)
        .putInt("last_http", srv.httpPort).putString("last_name", srv.name).apply()
}

private val addrPattern = Regex("^[A-Za-z0-9._-]+(?::\\d{1,5})?$")

internal fun parseManual(text: String, servers: List<Srv>): Srv? {
    val t = text.trim().removePrefix("https://").removePrefix("http://").trimEnd('/')
    if (!addrPattern.matches(t)) return null
    val host = t.substringBefore(':')
    val port = t.substringAfter(':', "").toIntOrNull() ?: 8443
    if (port !in 1..65535) return null
    val known = servers.firstOrNull { it.host == host }
    return Srv(known?.name ?: "手动输入", host, port, known?.httpPort ?: 8080)
}

private fun fmtBytes(b: Long): String = when {
    b >= 1024 * 1024 -> String.format("%.1f MB", b / 1048576.0)
    else -> "${b / 1024} KB"
}

private fun fmtDur(sec: Long): String = String.format("%02d:%02d", sec / 60, sec % 60)

@Composable
fun App() {
    val ctx = LocalContext.current
    val phase by MicState.phase.collectAsStateWithLifecycle()
    val servers by MicState.servers.collectAsStateWithLifecycle()
    val selected by MicState.selected.collectAsStateWithLifecycle()
    val active = phase is Phase.Connecting || phase is Phase.Streaming || phase is Phase.Reconnecting

    var manualText by remember { mutableStateOf("") }
    var pendingSrv by remember { mutableStateOf<Srv?>(null) }
    var localErr by remember { mutableStateOf<String?>(null) }
    var scanning by remember { mutableStateOf(false) }
    var refreshTick by remember { mutableIntStateOf(0) }
    var doDiscovery by remember { mutableStateOf<Discovery?>(null) }

    fun doStart(srv: Srv) {
        persistSelected(ctx, srv)
        MicState.selected.value = srv
        localErr = null
        startMic(ctx, srv)
    }

    val notifLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        pendingSrv?.let { doStart(it) }
        pendingSrv = null
    }
    val micLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        val srv = pendingSrv
        pendingSrv = null
        if (!granted) {
            localErr = "需要麦克风权限才能收音"
            return@rememberLauncherForActivityResult
        }
        if (srv == null) return@rememberLauncherForActivityResult
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            pendingSrv = srv
            notifLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            doStart(srv)
        }
    }
    val camLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) scanning = true else localErr = "需要相机权限才能扫码"
    }

    DisposableEffect(Unit) {
        val d = Discovery(ctx)
        doDiscovery = d
        d.start()
        onDispose {
            doDiscovery = null
            d.stop()
        }
    }
    LaunchedEffect(active, refreshTick) {
        while (!active && isActive) {
            delay(30_000)
            doDiscovery?.refresh(false)
        }
    }
    LaunchedEffect(Unit) {
        val p = prefs(ctx)
        val host = p.getString("last_host", null)
        if (host != null && MicState.selected.value == null) {
            MicState.selected.value = Srv(
                p.getString("last_name", host) ?: host, host,
                p.getInt("last_port", 8443), p.getInt("last_http", 8080),
            )
        }
    }

    Column(
        Modifier.fillMaxSize().background(Bg).systemBarsPadding()
            .verticalScroll(rememberScrollState()).padding(horizontal = 20.dp),
    ) {
        Spacer(Modifier.height(16.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier.size(44.dp).clip(RoundedCornerShape(13.dp))
                    .background(Brush.linearGradient(listOf(Indigo, IndigoBright))),
                contentAlignment = Alignment.Center,
            ) { MicGlyph(24.dp, Fg) }
            Spacer(Modifier.width(12.dp))
            Column {
                Text("phoneMic", color = Fg, fontSize = 21.sp, fontWeight = FontWeight.Bold)
                Text("这台手机，就是电脑的无线麦克风", color = Muted, fontSize = 12.5.sp)
            }
        }
        Spacer(Modifier.height(20.dp))

        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
            MicButton(phase) {
                if (active) {
                    stopMic(ctx)
                } else {
                    val srv = selected
                    if (srv == null) {
                        localErr = "先在下方选择或输入电脑地址"
                    } else if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
                        pendingSrv = srv
                        micLauncher.launch(Manifest.permission.RECORD_AUDIO)
                    } else if (Build.VERSION.SDK_INT >= 33 &&
                        ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
                    ) {
                        pendingSrv = srv
                        notifLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                    } else {
                        doStart(srv)
                    }
                }
            }
        }
        Spacer(Modifier.height(10.dp))
        Text(
            when (phase) {
                Phase.Idle -> "点击开始收音"
                Phase.Connecting -> "正在连接电脑…"
                Phase.Streaming -> "正在收音 · 点击停止"
                is Phase.Reconnecting -> "连接断开 · 自动重连中"
                is Phase.Failed -> "已停止"
            },
            color = Muted, fontSize = 14.sp, modifier = Modifier.fillMaxWidth(), textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(12.dp))
        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { StatusPill(phase) }

        val err = (phase as? Phase.Failed)?.msg ?: localErr
        if (err != null) {
            Spacer(Modifier.height(12.dp))
            Row(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp))
                    .background(Danger.copy(alpha = 0.12f)).padding(horizontal = 14.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(err, color = Danger, fontSize = 13.sp, modifier = Modifier.weight(1f))
                Text(
                    "✕", color = Muted, fontSize = 14.sp,
                    modifier = Modifier.clickable {
                        localErr = null
                        if (phase is Phase.Failed) MicState.phase.value = Phase.Idle
                    }.padding(6.dp),
                )
            }
        }

        Spacer(Modifier.height(16.dp))
        LevelCard()
        Spacer(Modifier.height(12.dp))
        if (!active) {
            ServerCard(servers, selected, manualText, { manualText = it }, {
                MicState.selected.value = it
                persistSelected(ctx, it)
            }, {
                val srv = parseManual(manualText, servers)
                if (srv == null) localErr = "地址格式: 192.168.x.x 或 IP:端口"
                else {
                    localErr = null
                    MicState.selected.value = srv
                    persistSelected(ctx, srv)
                }
            }, {
                refreshTick++
                doDiscovery?.refresh(true)
            }, {
                if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
                    scanning = true
                } else {
                    camLauncher.launch(Manifest.permission.CAMERA)
                }
            })
            Spacer(Modifier.height(12.dp))
        } else {
            ConnectedPcCard(selected)
            Spacer(Modifier.height(12.dp))
        }
        Tiles()
        Spacer(Modifier.height(16.dp))
        Text(
            "局域网直连 · 音频不出内网 · 48 kHz 单声道",
            color = Muted.copy(alpha = 0.7f), fontSize = 12.sp,
            modifier = Modifier.fillMaxWidth().padding(bottom = 20.dp), textAlign = TextAlign.Center,
        )
    }
    Text(
        "v" + BuildConfig.VERSION_NAME,
        color = Muted.copy(alpha = 0.7f), fontSize = 11.sp,
        modifier = Modifier.fillMaxSize().systemBarsPadding()
            .wrapContentSize(Alignment.BottomEnd).padding(end = 12.dp, bottom = 4.dp),
    )
    if (scanning) {
        ScanScreen(onResult = { raw ->
            scanning = false
            val srv = parseManual(raw, MicState.servers.value)
            if (srv == null) {
                localErr = "二维码不是电脑地址"
            } else {
                localErr = null
                MicState.selected.value = srv
                persistSelected(ctx, srv)
            }
        }, onClose = { scanning = false })
    }
}

@Composable
fun MicGlyph(size: androidx.compose.ui.unit.Dp, tint: Color) {
    Canvas(Modifier.size(size)) {
        val s = this.size.minDimension / 64f
        drawRoundRect(
            tint, topLeft = Offset(25 * s, 12 * s), size = Size(14 * s, 26 * s),
            cornerRadius = CornerRadius(7 * s, 7 * s),
        )
        drawArc(
            tint, 0f, 180f, false, topLeft = Offset(18 * s, 18 * s), size = Size(28 * s, 28 * s),
            style = Stroke(4 * s, cap = StrokeCap.Round),
        )
        drawLine(tint, Offset(32 * s, 46 * s), Offset(32 * s, 54 * s), 4 * s, StrokeCap.Round)
    }
}

@Composable
fun MicButton(phase: Phase, onClick: () -> Unit) {
    val streaming = phase is Phase.Streaming
    val connecting = phase is Phase.Connecting || phase is Phase.Reconnecting
    val level by MicState.level.collectAsStateWithLifecycle()
    val glow = (level * 2.2f).coerceIn(0f, 1f)
    val infinite = androidx.compose.animation.core.rememberInfiniteTransition()
    val pulse by infinite.animateFloat(
        0f, 1f,
        androidx.compose.animation.core.infiniteRepeatable(
            androidx.compose.animation.core.tween(1800, easing = androidx.compose.animation.core.LinearEasing),
            androidx.compose.animation.core.RepeatMode.Restart,
        ),
    )
    Box(Modifier.size(230.dp), contentAlignment = Alignment.Center) {
        Canvas(Modifier.matchParentSize()) {
            val c = center
            val base = size.minDimension / 2f
            if (streaming) {
                for (k in 0..1) {
                    val p = (pulse + k * 0.5f) % 1f
                    drawCircle(Green.copy(alpha = (1f - p) * 0.28f), base * (0.74f + p * 0.26f), c, style = Stroke(2.dp.toPx()))
                }
                drawCircle(
                    Brush.radialGradient(listOf(Green.copy(alpha = 0.10f + glow * 0.30f), Color.Transparent), c, base),
                    base * (0.8f + glow * 0.2f), c,
                )
            }
        }
        Box(
            Modifier.size(172.dp).clip(CircleShape)
                .background(
                    Brush.radialGradient(
                        if (streaming) listOf(Color(0xFF34D399), GreenDeep)
                        else listOf(IndigoBright, Indigo),
                    ),
                )
                .clickable(onClick = onClick),
            contentAlignment = Alignment.Center,
        ) {
            when {
                connecting -> CircularProgressIndicator(Modifier.size(52.dp), color = Fg, strokeWidth = 4.dp)
                streaming -> Canvas(Modifier.size(52.dp)) {
                    drawRoundRect(
                        Color(0xFF052E16), topLeft = Offset(size.minDimension * 0.28f, size.minDimension * 0.28f),
                        size = Size(size.minDimension * 0.44f, size.minDimension * 0.44f),
                        cornerRadius = CornerRadius(size.minDimension * 0.12f, size.minDimension * 0.12f),
                    )
                }
                else -> MicGlyph(52.dp, Fg)
            }
        }
    }
}

@Composable
fun StatusPill(phase: Phase) {
    val startedAt by MicState.startedAt.collectAsStateWithLifecycle()
    val active = phase is Phase.Connecting || phase is Phase.Streaming || phase is Phase.Reconnecting
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(active) {
        while (active) {
            now = System.currentTimeMillis()
            delay(500)
        }
    }
    val dotColor = when (phase) {
        Phase.Streaming -> Green
        Phase.Connecting -> IndigoBright
        is Phase.Reconnecting -> Amber
        is Phase.Failed -> Danger
        Phase.Idle -> Muted
    }
    val label = when (phase) {
        Phase.Idle -> "未连接"
        Phase.Connecting -> "连接中"
        Phase.Streaming -> "推流中"
        is Phase.Reconnecting -> "重连中·第${phase.attempt}次"
        is Phase.Failed -> "已停止"
    }
    Row(
        Modifier.clip(CircleShape).background(CardBg).padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(8.dp).clip(CircleShape).background(dotColor))
        Spacer(Modifier.width(8.dp))
        Text(label, color = Fg, fontSize = 13.5.sp, fontWeight = FontWeight.SemiBold)
        if (active && startedAt > 0) {
            Spacer(Modifier.width(10.dp))
            Text(fmtDur((now - startedAt) / 1000), color = Muted, fontSize = 13.sp)
        }
    }
}

@Composable
fun LevelCard() {
    val bars = remember { ArrayDeque(List(64) { 0f }) }
    var tick by remember { mutableIntStateOf(0) }
    var smooth by remember { mutableFloatStateOf(0f) }
    var dbText by remember { mutableStateOf("-- dB") }
    LaunchedEffect(Unit) {
        while (true) {
            androidx.compose.runtime.withFrameNanos { }
            val raw = MicState.level.value
            smooth = smooth * 0.7f + raw * 0.3f
            bars.removeFirst()
            bars.addLast((raw * 2.2f).coerceIn(0f, 1f))
            dbText = if (smooth > 0.0001f) "${(20 * log10(smooth)).toInt()} dB" else "-- dB"
            tick++
        }
    }
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(20.dp)).background(CardBg).padding(16.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("声音采集电平", color = Muted, fontSize = 13.sp, fontWeight = FontWeight.Medium)
            Spacer(Modifier.weight(1f))
            Text(dbText, color = Muted, fontSize = 12.5.sp)
        }
        Spacer(Modifier.height(10.dp))
        Canvas(Modifier.fillMaxWidth().height(88.dp)) {
            val t = tick
            if (t < 0) return@Canvas
            val mid = size.height / 2f
            drawLine(
                BorderC.copy(alpha = 0.6f), Offset(0f, mid), Offset(size.width, mid), 1.dp.toPx(),
                pathEffect = PathEffect.dashPathEffect(floatArrayOf(4.dp.toPx(), 5.dp.toPx())),
            )
            val bw = size.width / 64f
            bars.forEachIndexed { i, lvl ->
                val h = maxOf(2.5.dp.toPx(), lvl * size.height * 0.92f)
                drawRoundRect(
                    Green.copy(alpha = 0.30f + lvl * 0.70f),
                    topLeft = Offset(i * bw + bw * 0.22f, mid - h / 2f),
                    size = Size(bw * 0.56f, h),
                    cornerRadius = CornerRadius(bw * 0.28f, bw * 0.28f),
                )
            }
        }
    }
}

@Composable
fun ServerCard(
    servers: List<Srv>, selected: Srv?, manualText: String,
    onManualChange: (String) -> Unit, onSelect: (Srv) -> Unit, onManualOk: () -> Unit,
    onRefresh: () -> Unit, onScan: () -> Unit,
) {
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(20.dp)).background(CardBg).padding(16.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("连接到电脑", color = Fg, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
            Spacer(Modifier.weight(1f))
            Text(
                "刷新", color = Muted, fontSize = 13.sp,
                modifier = Modifier.clip(RoundedCornerShape(10.dp)).clickable(onClick = onRefresh)
                    .padding(horizontal = 10.dp, vertical = 8.dp),
            )
            Text(
                "扫码", color = Green, fontSize = 13.sp, fontWeight = FontWeight.SemiBold,
                modifier = Modifier.clip(RoundedCornerShape(10.dp)).clickable(onClick = onScan)
                    .padding(horizontal = 10.dp, vertical = 8.dp),
            )
        }
        Spacer(Modifier.height(10.dp))
        if (servers.isEmpty()) {
            Text("同一 WiFi 下自动搜索中… 可扫码、手动输入 IP 或点刷新", color = Muted, fontSize = 13.sp)
        }
        servers.forEach { srv ->
            val sel = selected?.host == srv.host && selected.port == srv.port
            Row(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
                    .background(if (sel) Green.copy(alpha = 0.10f) else Color.Transparent)
                    .clickable { onSelect(srv) }.padding(horizontal = 10.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(Modifier.size(8.dp).clip(CircleShape).background(if (sel) Green else Muted))
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(srv.name, color = Fg, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text("${srv.host}:${srv.port}", color = Muted, fontSize = 12.sp)
                }
                if (sel) Text("✓", color = Green, fontSize = 16.sp, fontWeight = FontWeight.Bold)
            }
        }
        Spacer(Modifier.height(10.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            OutlinedTextField(
                manualText, onManualChange, Modifier.weight(1f),
                placeholder = { Text("192.168.x.x[:8443]", color = Muted.copy(alpha = 0.6f), fontSize = 13.sp) },
                singleLine = true,
                shape = RoundedCornerShape(12.dp),
                textStyle = androidx.compose.ui.text.TextStyle(color = Fg, fontSize = 14.sp),
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = IndigoBright, unfocusedBorderColor = BorderC,
                    cursorColor = Green, focusedContainerColor = Bg, unfocusedContainerColor = Bg,
                ),
            )
            Spacer(Modifier.width(10.dp))
            Text(
                "使用", color = Green, fontSize = 14.sp, fontWeight = FontWeight.SemiBold,
                modifier = Modifier.clip(RoundedCornerShape(12.dp)).clickable(onClick = onManualOk)
                    .padding(horizontal = 14.dp, vertical = 12.dp),
            )
        }
        if (selected != null) {
            Spacer(Modifier.height(8.dp))
            Text("已选择: ${selected.host}:${selected.port}", color = Green, fontSize = 12.5.sp)
        }
    }
}

@Composable
fun ConnectedPcCard(selected: Srv?) {
    val phase by MicState.phase.collectAsStateWithLifecycle()
    val pc by MicState.pcStatus.collectAsStateWithLifecycle()
    val title = if (phase is Phase.Streaming) "已连接电脑" else "目标电脑"
    val name = pc?.computer?.takeIf { it.isNotEmpty() } ?: selected?.name ?: "-"
    val addr = selected?.let { "${it.host}:${it.port}" } ?: "-"
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(20.dp)).background(CardBg).padding(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, color = Muted, fontSize = 12.sp)
            Spacer(Modifier.height(4.dp))
            Text(name, color = Fg, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Spacer(Modifier.width(10.dp))
        Text(addr, color = Muted, fontSize = 12.5.sp)
    }
}

@Composable
fun Tile(title: String, value: String, modifier: Modifier = Modifier, onClick: (() -> Unit)? = null) {
    val base = modifier.clip(RoundedCornerShape(16.dp)).background(CardBg)
    Column(
        (if (onClick != null) base.clickable(onClick = onClick) else base).padding(14.dp),
    ) {
        Text(title, color = Muted, fontSize = 12.sp)
        Spacer(Modifier.height(4.dp))
        Text(value, color = Fg, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
fun Tiles() {
    val ctx = LocalContext.current
    val pc by MicState.pcStatus.collectAsStateWithLifecycle()
    val selected by MicState.selected.collectAsStateWithLifecycle()
    val sent by MicState.sentBytes.collectAsStateWithLifecycle()
    val rate by MicState.deviceRate.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    var devOpen by remember { mutableStateOf(false) }
    var devList by remember { mutableStateOf<PcDeviceList?>(null) }
    var devErr by remember { mutableStateOf<String?>(null) }
    var devBusy by remember { mutableStateOf(false) }
    var devTick by remember { mutableIntStateOf(0) }

    LaunchedEffect(devOpen, devTick) {
        if (!devOpen) return@LaunchedEffect
        val srv = selected
        if (srv == null) {
            devErr = "先在下方选择或输入电脑地址"
            return@LaunchedEffect
        }
        devErr = null
        try {
            devList = fetchPcDevices(ctx, srv)
        } catch (e: Exception) {
            android.util.Log.e("PcDevice", "fail, 设备列表获取失败, host: ${srv.host}, err: ${e.message ?: e.toString()}")
            devErr = "获取设备列表失败: " + (e.message ?: e.toString())
        }
    }

    val rateText = when {
        rate == 0 -> "48 kHz"
        rate == 48000 -> "48 kHz"
        else -> String.format("%.1f → 48 kHz", rate / 1000.0)
    }
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Tile(
                "声音写入设备(虚拟声卡)",
                if (pc?.device.isNullOrEmpty()) "点按选择" else pc!!.device,
                Modifier.weight(1f),
            ) { devOpen = true }
            Tile("电脑缓冲", "${pc?.bufferedMs ?: 0} ms", Modifier.weight(1f))
        }
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Tile("已发送", fmtBytes(sent), Modifier.weight(1f))
            Tile("采样率", rateText, Modifier.weight(1f))
        }
    }
    if (devOpen) {
        DevicePickDialog(
            list = devList, err = devErr, busy = devBusy,
            onRefresh = { devTick++ },
            onPick = { name ->
                val srv = selected
                if (srv == null || devBusy) return@DevicePickDialog
                scope.launch {
                    devBusy = true
                    devErr = null
                    try {
                        switchPcDevice(ctx, srv, name)
                        devList = fetchPcDevices(ctx, srv)
                    } catch (e: Exception) {
                        android.util.Log.e("PcDevice", "fail, 设备切换失败, host: ${srv.host}, name: $name, err: ${e.message ?: e.toString()}")
                        devErr = "切换声音写入设备失败: " + (e.message ?: e.toString())
                    }
                    devBusy = false
                }
            },
            onDismiss = { devOpen = false },
        )
    }
}

@Composable
fun DevicePickDialog(
    list: PcDeviceList?, err: String?, busy: Boolean,
    onRefresh: () -> Unit, onPick: (String) -> Unit, onDismiss: () -> Unit,
) {
    val selName = pickedDeviceName(list)
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = CardBg,
        title = { Text("选择声音写入设备", color = Fg, fontSize = 16.sp, fontWeight = FontWeight.SemiBold) },
        text = {
            Column(Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState())) {
                if (list == null && err == null) Text("加载中…", color = Muted, fontSize = 13.sp)
                list?.let { l ->
                    DeviceRow("自动(虚拟声卡)", "跟随电脑默认虚拟声卡", selName == "", busy) { onPick("") }
                    l.devices.forEach { d ->
                        DeviceRow(d.name, if (d.isDefault) "系统默认" else "", selName == d.name, busy) { onPick(d.name) }
                    }
                }
                if (err != null) {
                    Spacer(Modifier.height(8.dp))
                    Text(err, color = Danger, fontSize = 12.5.sp)
                }
                if (list != null && list.current.isNotEmpty() && !isVirtualDevice(list.current)) {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "正在写入物理扬声器：手机声音会从音响外放，可能被手机再次拾取产生啸叫。建议选虚拟声卡。",
                        color = Amber, fontSize = 12.sp,
                        modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp))
                            .background(Amber.copy(alpha = 0.10f)).padding(10.dp),
                    )
                }
            }
        },
        confirmButton = {
            Text(
                "关闭", color = Muted, fontSize = 14.sp,
                modifier = Modifier.clip(RoundedCornerShape(10.dp)).clickable(onClick = onDismiss)
                    .padding(horizontal = 14.dp, vertical = 12.dp),
            )
        },
        dismissButton = {
            Text(
                if (busy) "刷新中…" else "刷新", color = Green, fontSize = 14.sp, fontWeight = FontWeight.SemiBold,
                modifier = Modifier.clip(RoundedCornerShape(10.dp)).clickable(enabled = !busy, onClick = onRefresh)
                    .padding(horizontal = 14.dp, vertical = 12.dp),
            )
        },
    )
}

@Composable
private fun DeviceRow(name: String, sub: String, sel: Boolean, busy: Boolean, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
            .background(if (sel) Green.copy(alpha = 0.10f) else Color.Transparent)
            .clickable(enabled = !busy, onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 13.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(8.dp).clip(CircleShape).background(if (sel) Green else Muted))
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(name, color = Fg, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (sub.isNotEmpty()) Text(sub, color = Muted, fontSize = 12.sp)
        }
        if (sel) Text("✓", color = Green, fontSize = 16.sp, fontWeight = FontWeight.Bold)
    }
}