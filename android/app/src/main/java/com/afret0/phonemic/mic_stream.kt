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
import kotlin.coroutines.coroutineContext
import kotlin.math.roundToInt
import kotlin.math.sqrt

private const val TAG = "MicEngine"
private const val SAMPLE_RATE = 48000
private const val FRAME_SAMPLES = 960
private const val FRAME_BYTES = FRAME_SAMPLES * 2
private const val WS_QUEUE_DROP = 256 * 1024

class Resampler(srcRate: Int) {
    private val stepUnits = srcRate.toLong()
    private var posUnits = 0L
    private var prev: Short = 0

    fun process(input: ShortArray, n: Int, emit: (Short) -> Unit) {
        if (n <= 0) return
        val rate = SAMPLE_RATE.toLong()
        val limit = (n - 1).toLong() * rate
        while (posUnits < limit) {
            val i = posUnits.floorDiv(rate).toInt()
            val frac = Math.floorMod(posUnits, rate).toDouble() / SAMPLE_RATE
            val a = if (i < 0) prev else input[i]
            val b = input[i + 1]
            emit((a + (b - a) * frac).roundToInt().coerceIn(-32768, 32767).toShort())
            posUnits += stepUnits
        }
        prev = input[n - 1]
        posUnits -= n.toLong() * rate
    }
}

class MicEngine(private val ctx: Context) {

    suspend fun run(srv: Srv) = coroutineScope {
        MicState.resetSession()
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
        val ws = client.newWebSocket(
            Request.Builder().url("wss://${srv.host}:${srv.port}/ws").build(),
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
            Log.i(TAG, "done, WS 已连接开始采集, host: ${srv.host}")
            captureLoop(ws, closed)
            return if (closed.isCompleted) closed.await() else -1
        } finally {
            ws.cancel()
        }
    }

    private fun openRecord(): AudioRecord {
        val minBuf = AudioRecord.getMinBufferSize(SAMPLE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT)
        val bufBytes = maxOf(minBuf, FRAME_BYTES * 8)
        for (src in intArrayOf(9, 6, 1)) {
            val r = runCatching {
                AudioRecord(src, SAMPLE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT, bufBytes)
            }.getOrNull()
            if (r != null && r.state == AudioRecord.STATE_INITIALIZED) {
                Log.i(TAG, "hit, 麦克风已打开, source: $src, rate: ${r.sampleRate}")
                return r
            }
            r?.release()
            Log.i(TAG, "skip, 采集源不可用换下一个, source: $src")
        }
        error("无法打开麦克风")
    }

    private suspend fun captureLoop(ws: WebSocket, closed: CompletableDeferred<Int>) = withContext(Dispatchers.IO) {
        val record = openRecord()
        try {
            MicState.deviceRate.value = record.sampleRate
            val resampler = if (record.sampleRate != SAMPLE_RATE) Resampler(record.sampleRate) else null
            record.startRecording()
            val chunk = ShortArray(FRAME_SAMPLES)
            val pending = ByteArray(FRAME_BYTES * 4)
            var pendingN = 0
            var dropped = 0

            fun emitSample(s: Short) {
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
                val n = record.read(chunk, 0, FRAME_SAMPLES)
                if (n <= 0) continue
                if (resampler == null) {
                    for (i in 0 until n) emitSample(chunk[i])
                } else {
                    resampler.process(chunk, n) { emitSample(it) }
                }
            }
            record.stop()
        } finally {
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
                            device = j.optString("device", "-"),
                            bufferedMs = j.optInt("bufferedMs", 0),
                            streaming = j.optBoolean("streaming", false),
                            sessionBytes = j.optLong("sessionBytes", 0),
                        )
                    }
                }
            }
            delay(2000)
        }
    }
}