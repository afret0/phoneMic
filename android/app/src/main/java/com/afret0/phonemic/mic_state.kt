package com.afret0.phonemic

import kotlinx.coroutines.flow.MutableStateFlow

data class Srv(val name: String, val host: String, val port: Int, val httpPort: Int)

data class PcStatus(val computer: String, val device: String, val bufferedMs: Int, val streaming: Boolean, val sessionBytes: Long, val mode: String = "mic")

enum class StreamMode { Mic, Speaker }

fun parseMode(s: String): StreamMode = if (s == "speaker") StreamMode.Speaker else StreamMode.Mic

sealed interface Phase {
    data object Idle : Phase
    data object Connecting : Phase
    data object Streaming : Phase
    data class Reconnecting(val attempt: Int, val reason: String) : Phase
    data class Failed(val msg: String) : Phase
}

object MicState {
    val phase = MutableStateFlow<Phase>(Phase.Idle)
    val mode = MutableStateFlow(StreamMode.Mic)
    val level = MutableStateFlow(0f)
    val sentBytes = MutableStateFlow(0L)
    val receivedBytes = MutableStateFlow(0L)
    val spkBufferedMs = MutableStateFlow(0)
    val startedAt = MutableStateFlow(0L)
    val deviceRate = MutableStateFlow(0)
    val servers = MutableStateFlow<List<Srv>>(emptyList())
    val pcStatus = MutableStateFlow<PcStatus?>(null)
    val selected = MutableStateFlow<Srv?>(null)
    val gain = MutableStateFlow(1f)
    val agcOn = MutableStateFlow(false)
    val agcActive = MutableStateFlow(false)
    val agcAvailable = MutableStateFlow(true)
    val lowLatency = MutableStateFlow(false)

    fun resetSession() {
        level.value = 0f
        sentBytes.value = 0L
        receivedBytes.value = 0L
        spkBufferedMs.value = 0
        startedAt.value = 0L
        deviceRate.value = 0
        pcStatus.value = null
    }
}