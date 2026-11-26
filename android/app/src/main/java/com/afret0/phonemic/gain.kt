package com.afret0.phonemic

import android.content.Context
import android.media.audiofx.AudioEffect
import android.media.audiofx.AutomaticGainControl
import android.util.Log
import kotlin.math.pow
import kotlin.math.roundToInt
import kotlin.math.tanh

private const val TAG = "MicGain"
private const val GAIN_KEY = "mic_gain"
private const val AGC_KEY = "mic_agc"
private const val LOWLAT_KEY = "mic_lowlat"
private const val KNEE = 0.75f
private const val NORM = 0.9760502f

class SoftLimiter {
    fun process(v: Float): Float {
        val a = if (v < 0f) -v else v
        if (a <= KNEE) return v
        val sign = if (v < 0f) -1f else 1f
        return sign * (KNEE + (1f - KNEE) * tanh((a - KNEE) / (1f - KNEE)))
    }
}

fun gainFromSlider(v: Float): Float = 10f.pow(v.coerceIn(0f, 1f))

fun sliderFromGain(g: Float): Float {
    val v = kotlin.math.log10(g.coerceIn(1f, 10f))
    return v.coerceIn(0f, 1f)
}

fun fmtGain(g: Float): String = if (g < 9.95f) String.format("%.1fx", g) else "10x"

fun applyGainSoft(s: Short, gain: Float, limiter: SoftLimiter): Short {
    if (gain <= 1.0001f) return s
    val f = limiter.process(s / 32768f * gain)
    return (f * 32768f).roundToInt().coerceIn(-32768, 32767).toShort()
}

fun loadGain(ctx: Context): Float =
    gainFromSlider(ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE).getFloat(GAIN_KEY, 0f))

fun saveGain(ctx: Context, slider: Float) {
    ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE).edit().putFloat(GAIN_KEY, slider).apply()
}

fun loadAgc(ctx: Context): Boolean =
    ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE).getBoolean(AGC_KEY, false)

fun saveAgc(ctx: Context, on: Boolean) {
    ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE).edit().putBoolean(AGC_KEY, on).apply()
}

fun loadLowLat(ctx: Context): Boolean =
    ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE).getBoolean(LOWLAT_KEY, false)

fun saveLowLat(ctx: Context, on: Boolean) {
    ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE).edit().putBoolean(LOWLAT_KEY, on).apply()
}

interface AgcHandle {
    val active: Boolean
    fun setOn(on: Boolean): Boolean
    fun release()
}

fun attachAgc(sessionId: Int, wantOn: Boolean): AgcHandle = try {
    if (!AutomaticGainControl.isAvailable()) {
        Log.i(TAG, "skip, 系统未提供自动增益效果, wantOn: $wantOn")
        NoAgc
    } else {
        SessionAgc(sessionId)
    }
} catch (e: Exception) {
    Log.e(TAG, "fail, 自动增益初始化失败, sessionId: $sessionId, err: ${e.message ?: e.javaClass.simpleName}")
    NoAgc
}

private object NoAgc : AgcHandle {
    override val active = false
    override fun setOn(on: Boolean) = false
    override fun release() {}
}

private class SessionAgc(sessionId: Int) : AgcHandle {
    private var effect: AutomaticGainControl? = runCatching {
        AutomaticGainControl.create(sessionId)
    }.onFailure {
        Log.e(TAG, "fail, 自动增益效果创建失败, sessionId: $sessionId, err: ${it.message ?: it.javaClass.simpleName}")
    }.getOrNull()

    override val active get() = effect != null

    init {
        if (effect == null) Log.i(TAG, "skip, 自动增益效果不可用于本采集会话, sessionId: $sessionId")
    }

    override fun setOn(on: Boolean): Boolean {
        val e = effect ?: return false
        return runCatching { e.setEnabled(on) == AudioEffect.SUCCESS }
            .onFailure { Log.e(TAG, "fail, 自动增益开关失败, on: $on, err: ${it.message ?: it.javaClass.simpleName}") }
            .getOrDefault(false)
            .also { ok -> Log.i(TAG, "done, 自动增益开关, on: $on, ok: $ok") }
    }

    override fun release() {
        runCatching { effect?.release() }
        effect = null
    }
}