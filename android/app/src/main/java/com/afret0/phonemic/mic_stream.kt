package com.afret0.phonemic

import android.content.Context
import android.media.AudioFormat
import android.media.AudioRecord
import android.util.Log
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.json.JSONObject
import java.net.URLEncoder
import kotlin.coroutines.coroutineContext
import kotlin.math.roundToInt
import kotlin.math.sqrt

private const val TAG = "MicEngine"
private const val SAMPLE_RATE = 48000
private const val FRAME_SAMPLES = 960
private const val FRAME_BYTES = FRAME_SAMPLES * 2
private const val WS_QUEUE_DROP = 256 * 1024

class Resampler(srcRate: Int) {
    private val cutoff = minOf(1.0, SAMPLE_RATE.toDouble() / srcRate)
    private val baseUnits = srcRate.toLong() * FINE
    private val unitRate = SAMPLE_RATE.toLong() * FINE
    private var stepUnits = baseUnits
    private var trimPpm = 0
    private var posUnits = 0L
    private val hist = ShortArray(TAPS - 1)
    private val kernel = Array(PHASES) { p ->
        val frac = p.toDouble() / PHASES
        val w = DoubleArray(TAPS) { j ->
            val t = frac + (TAPS / 2 - 1) - j
            val x = Math.PI * cutoff * t
            val sinc = if (x == 0.0) 1.0 else kotlin.math.sin(x) / x
            val win = 0.42 - 0.5 * kotlin.math.cos(2 * Math.PI * j / (TAPS - 1)) + 0.08 * kotlin.math.cos(4 * Math.PI * j / (TAPS - 1))
            sinc * win
        }
        val sum = w.sum()
        DoubleArray(TAPS) { w[it] / sum }
    }

    fun setTrim(ppm: Int) {
        val p = ppm.coerceIn(-500, 500)
        if (p == trimPpm) return
        trimPpm = p
        stepUnits = baseUnits * (1_000_000L + p) / 1_000_000L
    }

    fun process(input: ShortArray, n: Int, emit: (Short) -> Unit) {
        if (n <= 0) return
        if (stepUnits == unitRate) {
            val limit = (n - 1).toLong() * unitRate
            while (posUnits < limit) {
                val i = posUnits.floorDiv(unitRate).toInt()
                emit(if (i < 0) hist[TAPS - 1 + i] else input[i])
                posUnits += unitRate
            }
        } else {
            val limit = (n - TAPS / 2).toLong() * unitRate
            while (posUnits < limit) {
                val i = posUnits.floorDiv(unitRate).toInt()
                val phase = (Math.floorMod(posUnits, unitRate) * PHASES / unitRate).toInt()
                val k = kernel[phase]
                var acc = 0.0
                for (j in 0 until TAPS) {
                    val idx = i - (TAPS / 2 - 1) + j
                    val s = if (idx < 0) hist[TAPS - 1 + idx] else input[idx]
                    acc += s * k[j]
                }
                emit(acc.roundToInt().coerceIn(-32768, 32767).toShort())
                posUnits += stepUnits
            }
        }
        for (j in 0 until TAPS - 1) {
            val idx = n - (TAPS - 1) + j
            hist[j] = if (idx < 0) hist[TAPS - 1 + idx] else input[idx]
        }
        posUnits -= n.toLong() * unitRate
    }

    companion object {
        private const val FINE = 1_000_000L
        private const val TAPS = 8
        private const val PHASES = 147
    }
}

class DriftTrim {
    var ema: Double? = null
        private set
    var ppm: Int = 0
        private set
    private var targetMs = 100

    fun setTarget(ms: Int) {
        if (ms == targetMs) return
        targetMs = ms
        reset()
    }

    fun reset() {
        ema = null
        ppm = 0
    }

    fun update(bufferedMs: Int): Int {
        val prev = ema
        val e = if (prev == null) bufferedMs.toDouble() else prev + 0.3 * (bufferedMs - prev)
        ema = e
        val err = e - targetMs
        ppm = if (Math.abs(err) > 20) Math.round(err * 10).toInt().coerceIn(-500, 500) else 0
        return ppm
    }
}

class MicEngine(private val ctx: Context) {
    private val drift = DriftTrim()
    @Volatile private var trimPpm = 0

    suspend fun run(srv: Srv) = coroutineScope {
        MicState.resetSession()
        drift.reset()
        trimPpm = 0
        Log.i(TAG, "recv, 推流会话开始, host: ${srv.host}, port: ${srv.port}")
        val pollJob = launch { pollStatus(srv) }
        var attempt = 0
        var lastReason = ""
        var fatal: String? = null

        while (coroutineContext.isActive) {
            MicState.phase.value = if (attempt == 0 && MicState.startedAt.value == 0L) Phase.Connecting else Phase.Reconnecting(attempt, lastReason)
            try {
                val client = CaStore.ensureTrusted(ctx, srv)
                val code = streamOnce(client, srv)
                attempt = 0
                if (code == 4001) {
                    Log.i(TAG, "skip, 被新的推流设备替换, code: 4001")
                    fatal = "已被新的推流设备替换"
                    break
                }
                lastReason = "连接断开(code $code)"
                Log.e(TAG, "fail, 会话结束进入重连, code: $code")
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                lastReason = e.message ?: e.javaClass.simpleName
                Log.e(TAG, "fail, 推流异常进入重连, err: $lastReason")
            }
            attempt++
            val backoffMs = minOf(30000L, 1000L shl minOf(attempt - 1, 5))
            MicState.phase.value = Phase.Reconnecting(attempt, lastReason)
            Log.i(TAG, "hit, 退避后重连, attempt: $attempt, backoffMs: $backoffMs")
            delay(backoffMs)
        }
        pollJob.cancelAndJoin()
        MicState.level.value = 0f
        MicState.phase.value = fatal?.let { Phase.Failed(it) } ?: Phase.Idle
        Log.i(TAG, "done, 推流会话退出, sentBytes: ${MicState.sentBytes.value}, fatal: $fatal")
    }

    private suspend fun streamOnce(client: OkHttpClient, srv: Srv): Int {
        val opened = CompletableDeferred<Unit>()
        val closed = CompletableDeferred<Int>()
        val name = localDeviceName(ctx)
        val wsUrl = "wss://${srv.host}:${srv.port}/ws?name=" + URLEncoder.encode(name, "UTF-8") +
            (if (MicState.lowLatency.value) "&lat=low" else "")
        val ws = client.newWebSocket(
            Request.Builder().url(wsUrl).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    opened.complete(Unit)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    opened.completeExceptionally(t)
                    closed.complete(-1)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(1000, null)
                    closed.complete(code)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    closed.complete(code)
                }
            },
        )
        try {
            withTimeout(8000) { opened.await() }
            if (MicState.startedAt.value == 0L) MicState.startedAt.value = System.currentTimeMillis()
            MicState.phase.value = Phase.Streaming
            Log.i(TAG, "done, WS 已连接开始采集, host: ${srv.host}, name: $name")
            captureLoop(ws, closed)
            return if (closed.isCompleted) closed.await() else -1
        } finally {
            ws.cancel()
        }
    }

    private fun openRecord(): AudioRecord {
        val minBuf = AudioRecord.getMinBufferSize(SAMPLE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT)
        val bufBytes = maxOf(minBuf, FRAME_BYTES * 8)
        val sources = if (MicState.agcOn.value) intArrayOf(7, 1, 6, 9) else intArrayOf(1, 6, 9)
        for (src in sources) {
            val r = runCatching {
                AudioRecord(src, SAMPLE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT, bufBytes)
            }.getOrNull()
            if (r != null && r.state == AudioRecord.STATE_INITIALIZED) {
                Log.i(TAG, "hit, 麦克风已打开, source: $src, rate: ${r.sampleRate}, agcOn: ${MicState.agcOn.value}")
                return r
            }
            r?.release()
            Log.i(TAG, "skip, 采集源不可用换下一个, source: $src")
        }
        error("无法打开麦克风")
    }

    private suspend fun captureLoop(ws: WebSocket, closed: CompletableDeferred<Int>) = withContext(Dispatchers.IO) {
        val record = openRecord()
        var agc: AgcHandle? = null
        try {
            MicState.deviceRate.value = record.sampleRate
            val resampler = Resampler(record.sampleRate)
            record.startRecording()
            val chunk = ShortArray(FRAME_SAMPLES)
            val pending = ByteArray(FRAME_BYTES * 4)
            var pendingN = 0
            var dropped = 0
            var curGain = MicState.gain.value
            val limiter = SoftLimiter()
            val agcHandle = attachAgc(record.audioSessionId, MicState.agcOn.value)
            agc = agcHandle
            MicState.agcAvailable.value = agcHandle.active
            var agcApplied = false
            var agcSwitchAt = 0L

            fun emitSample(raw: Short) {
                val target = MicState.gain.value
                curGain = if (curGain == target) target else curGain + (target - curGain) * 0.005f
                val s = applyGainSoft(raw, curGain, limiter)
                pending[pendingN] = (s.toInt() and 0xFF).toByte()
                pending[pendingN + 1] = ((s.toInt() shr 8) and 0xFF).toByte()
                pendingN += 2
                if (pendingN >= FRAME_BYTES) {
                    var sum = 0.0
                    for (i in 0 until FRAME_SAMPLES) {
                        val v = ((pending[i * 2].toInt() and 0xFF) or (pending[i * 2 + 1].toInt() shl 8)).toShort()
                        val f = v / 32768.0
                        sum += f * f
                    }
                    MicState.level.value = sqrt(sum / FRAME_SAMPLES).toFloat()
                    if (ws.queueSize() > WS_QUEUE_DROP) {
                        dropped++
                        if (dropped % 50 == 1) Log.i(TAG, "skip, 发送队列积压丢帧, queue: ${ws.queueSize()}, dropped: $dropped")
                    } else if (!ws.send(pending.copyOf(FRAME_BYTES).toByteString())) {
                        closed.complete(-2)
                    } else {
                        MicState.sentBytes.value += FRAME_BYTES
                    }
                    pendingN = 0
                }
            }

            while (coroutineContext.isActive && !closed.isCompleted) {
                if (agcHandle.active && MicState.agcOn.value != agcApplied) {
                    val on = MicState.agcOn.value
                    Log.i(TAG, "hit, 自动增益开关切换, old: $agcApplied, new: $on")
                    if (on) {
                        agcApplied = agcHandle.setOn(true)
                        agcSwitchAt = 0L
                        if (!agcApplied) MicState.agcOn.value = false
                    } else {
                        agcHandle.setOn(false)
                        agcSwitchAt = System.currentTimeMillis() + 300
                        agcApplied = false
                    }
                }
                if (agcSwitchAt != 0L && System.currentTimeMillis() >= agcSwitchAt) {
                    Log.i(TAG, "hit, 自动增益已关, 重开采集回到主麦路径")
                    break
                }
                val n = record.read(chunk, 0, FRAME_SAMPLES)
                if (n <= 0) continue
                resampler.setTrim(trimPpm)
                resampler.process(chunk, n) { emitSample(it) }
            }
            record.stop()
        } finally {
            agc?.release()
            MicState.agcAvailable.value = true
            record.release()
        }
    }

    private suspend fun pollStatus(srv: Srv) = withContext(Dispatchers.IO) {
        var client: OkHttpClient? = null
        var hadCa = false
        while (coroutineContext.isActive) {
            runCatching {
                val hasCa = CaStore.storedCa(ctx, srv.host) != null
                if (client == null || hasCa != hadCa) {
                    client = CaStore.client(ctx, srv.host)
                    hadCa = hasCa
                }
                client!!.newCall(Request.Builder().url("https://${srv.host}:${srv.port}/status").build()).execute().use { r ->
                    if (r.isSuccessful) {
                        val j = JSONObject(r.body?.string() ?: "{}")
                        MicState.pcStatus.value = PcStatus(
                            computer = j.optString("computer", ""),
                            device = j.optString("device", "-"),
                            bufferedMs = j.optInt("bufferedMs", 0),
                            streaming = j.optBoolean("streaming", false),
                            sessionBytes = j.optLong("sessionBytes", 0),
                        )
                        if (j.optBoolean("streaming", false) && j.has("bufferedMs")) {
                            val bufferedMs = j.optInt("bufferedMs", 0)
                            drift.setTarget(j.optInt("bufferedTargetMs", 100))
                            val old = drift.ppm
                            val next = drift.update(bufferedMs)
                            if (next != old) Log.i(TAG, "hit, 时钟漂移微调, bufferedMs: $bufferedMs, ema: ${drift.ema}, trimPpm: $old -> $next")
                            trimPpm = next
                        } else {
                            if (trimPpm != 0) Log.i(TAG, "hit, 推流停止重置微调, trimPpm: $trimPpm -> 0")
                            drift.reset()
                            trimPpm = 0
                        }
                    }
                }
            }
            delay(2000)
        }
    }
}