package com.afret0.phonemic

import android.content.Context
import android.os.Build
import android.provider.Settings
import android.util.Log
import java.security.SecureRandom

private const val TAG = "DeviceName"
private const val LOCAL_NAME_KEY = "local_name"
private const val RANDOM_ALPHABET = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
private const val MAX_NAME_LEN = 64

internal fun buildDeviceName(manufacturer: String?, model: String?): String {
    val m = manufacturer?.trim().orEmpty()
    val md = model?.trim().orEmpty()
    val name = if (m.isNotEmpty() && !md.startsWith(m, ignoreCase = true)) "$m $md" else md
    return name.trim()
}

internal fun pickDeviceName(settingsName: String?, manufacturer: String?, model: String?, saved: String?): String {
    val s = settingsName?.trim().orEmpty()
    if (s.isNotEmpty()) return s.take(MAX_NAME_LEN)
    val built = buildDeviceName(manufacturer, model)
    if (built.isNotEmpty()) return built.take(MAX_NAME_LEN)
    return saved?.trim()?.takeIf { it.isNotEmpty() }?.take(MAX_NAME_LEN).orEmpty()
}

internal fun randomPhoneName(): String {
    val rnd = SecureRandom()
    return "Phone-" + (1..4).map { RANDOM_ALPHABET[rnd.nextInt(RANDOM_ALPHABET.length)] }.joinToString("")
}

internal fun localDeviceName(ctx: Context): String {
    val settingsName = try {
        Settings.Global.getString(ctx.contentResolver, Settings.Global.DEVICE_NAME)
    } catch (e: Exception) {
        Log.e(TAG, "fail, 读取系统设备名失败, err: ${e.message ?: e.javaClass.simpleName}")
        null
    }
    val prefs = ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE)
    val picked = pickDeviceName(settingsName, Build.MANUFACTURER, Build.MODEL, prefs.getString(LOCAL_NAME_KEY, null))
    if (picked.isNotEmpty()) {
        val source = when {
            settingsName?.trim().orEmpty().isNotEmpty() -> "系统设备名"
            buildDeviceName(Build.MANUFACTURER, Build.MODEL).isNotEmpty() -> "厂商型号"
            else -> "已保存随机名"
        }
        Log.i(TAG, "hit, 本机名已确定, name: $picked, 来源: $source")
        return picked
    }
    val name = randomPhoneName()
    prefs.edit().putString(LOCAL_NAME_KEY, name).apply()
    Log.i(TAG, "done, 设备名不可得, 已生成随机本机名并持久化, name: $name")
    return name
}