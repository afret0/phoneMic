package com.afret0.phonemic

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioTrack
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
import java.net.URLEncoder
import kotlin.coroutines.coroutineContext
import kotlin.math.sqrt

private const val TAG = "SpkEngine"
private const val SAMPLE_RATE = 48000
private const val FRAME_SAMPLES = 960

class SpeakerEngine(private val ctx: Context) {
    private val buffer = SpeakerBuffer()

    suspend fun run(srv: Srv) = coroutineScope {
        MicState.resetSession()
        buffer.reset()
        Log.i(TAG, "recv, 收听会话开始, host: ${srv.host}, port: ${srv.port}")
        val pollJob = launch { pollSpeakerStatus(srv) }
        runSession(ctx, srv, TAG) { client, s -> playOnce(client, s) }
        pollJob.cancelAndJoin()
        MicState.spkBufferedMs.value = 0
        Log.i(TAG, "done, 收听会话退出, receivedBytes: ${MicState.receivedBytes.value}, droppedBytes: ${buffer.droppedBytes}, underrunBytes: ${buffer.underrunBytes}, phase: ${MicState.phase.value}")
    }

    private suspend fun playOnce(client: OkHttpClient, srv: Srv): Int {
        val opened = CompletableDeferred<Unit>()
        val closed = CompletableDeferred<Int>()
        val name = localDeviceName(ctx)
        val wsUrl = "wss://${srv.host}:${srv.port}/speaker?name=" + URLEncoder.encode(name, "UTF-8")
        val ws = client.newWebSocket(
            Request.Builder().url(wsUrl).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    opened.complete(Unit)
                }

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    MicState.receivedBytes.value += bytes.size
                    buffer.push(bytes.toByteArray())
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
            buffer.reset()
            if (MicState.startedAt.value == 0L) MicState.startedAt.value = System.currentTimeMillis()
            MicState.phase.value = Phase.Streaming
            Log.i(TAG, "done, WS 已连接开始收听, host: ${srv.host}, name: $name")
            playLoop(ws, closed)
            return if (closed.isCompleted) closed.await() else -1
        } finally {
            ws.cancel()
        }
    }

    private fun openTrack(): AudioTrack {
        val minBuf = AudioTrack.getMinBufferSize(SAMPLE_RATE, AudioFormat.CHANNEL_OUT_MONO, AudioFormat.ENCODING_PCM_16BIT)
        val bufBytes = maxOf(minBuf, FRAME_SAMPLES * 2 * 8)
        val t = AudioTrack(
            AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_MEDIA)
                .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                .build(),
            AudioFormat.Builder()
                .setSampleRate(SAMPLE_RATE)
                .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
                .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                .build(),
            bufBytes, AudioTrack.MODE_STREAM, AudioManager.AUDIO_SESSION_ID_GENERATE,
        )
        if (t.state != AudioTrack.STATE_INITIALIZED) {
            t.release()
            error("无法打开音频播放")
        }
        Log.i(TAG, "hit, 播放轨道已打开, rate: $SAMPLE_RATE, bufBytes: $bufBytes")
        return t
    }

    private suspend fun playLoop(ws: WebSocket, closed: CompletableDeferred<Int>) = withContext(Dispatchers.IO) {
        val track = openTrack()
        try {
            track.play()
            val chunk = ShortArray(FRAME_SAMPLES)
            val limiter = SoftLimiter()
            var curGain = MicState.gain.value
            while (coroutineContext.isActive && !closed.isCompleted) {
                val got = buffer.read(chunk)
                MicState.spkBufferedMs.value = buffer.bufferedMs()
                if (got == 0) {
                    delay(5)
                    continue
                }
                var sum = 0.0
                for (i in 0 until got) {
                    val f = chunk[i] / 32768.0
                    sum += f * f
                }
                MicState.level.value = sqrt(sum / got).toFloat()
                val target = MicState.gain.value
                for (i in 0 until got) {
                    curGain = if (curGain == target) target else curGain + (target - curGain) * 0.005f
                    chunk[i] = applyGainSoft(chunk[i], curGain, limiter)
                }
                var off = 0
                while (off < got && coroutineContext.isActive && !closed.isCompleted) {
                    val n = track.write(chunk, off, got - off)
                    if (n <= 0) {
                        Log.e(TAG, "fail, 播放写入失败, n: $n")
                        closed.complete(-2)
                        break
                    }
                    off += n
                }
            }
            track.stop()
        } finally {
            track.release()
        }
    }

    private suspend fun pollSpeakerStatus(srv: Srv) = withContext(Dispatchers.IO) {
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
                    if (r.isSuccessful) MicState.pcStatus.value = parseStatus(r.body?.string() ?: "{}")
                }
            }
            delay(2000)
        }
    }
}