package com.afret0.phonemic

import android.content.Context
import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.TimeUnit

private const val TAG = "PcDevice"

data class PcDevice(val name: String, val isDefault: Boolean)

data class PcDeviceList(val devices: List<PcDevice>, val auto: Boolean, val current: String)

internal fun parseDevices(json: String): PcDeviceList {
    val j = JSONObject(json)
    val arr = j.optJSONArray("devices") ?: JSONArray()
    val list = ArrayList<PcDevice>(arr.length())
    for (i in 0 until arr.length()) {
        val o = arr.getJSONObject(i)
        val name = o.optString("name", "")
        if (name.isNotEmpty()) list.add(PcDevice(name, o.optBoolean("isDefault", false)))
    }
    return PcDeviceList(list, j.optBoolean("auto", true), j.optString("current", ""))
}

internal fun isVirtualDevice(name: String): Boolean =
    Regex("cable|blackhole", RegexOption.IGNORE_CASE).containsMatchIn(name)

internal fun pickedDeviceName(list: PcDeviceList?): String =
    if (list == null || list.auto || list.current.isEmpty()) "" else list.current

suspend fun fetchPcDevices(ctx: Context, srv: Srv): PcDeviceList = withContext(Dispatchers.IO) {
    val client = CaStore.ensureTrusted(ctx, srv).newBuilder().callTimeout(8, TimeUnit.SECONDS).build()
    client.newCall(Request.Builder().url("https://${srv.host}:${srv.port}/devices").build()).execute().use { r ->
        if (!r.isSuccessful) error("获取设备列表失败 HTTP ${r.code}")
        val parsed = parseDevices(r.body?.string() ?: "{}")
        Log.i(TAG, "done, 设备列表已获取, host: ${srv.host}, count: ${parsed.devices.size}, auto: ${parsed.auto}, current: ${parsed.current}")
        parsed
    }
}

suspend fun switchPcDevice(ctx: Context, srv: Srv, name: String): String = withContext(Dispatchers.IO) {
    val client = CaStore.ensureTrusted(ctx, srv).newBuilder().callTimeout(8, TimeUnit.SECONDS).build()
    val body = JSONObject().put("name", name).toString().toRequestBody("application/json".toMediaType())
    client.newCall(Request.Builder().url("https://${srv.host}:${srv.port}/device").post(body).build()).execute().use { r ->
        val text = r.body?.string() ?: ""
        if (!r.isSuccessful) error(text.ifEmpty { "切换失败 HTTP ${r.code}" })
        Log.i(TAG, "done, 电脑声音设备已切换, host: ${srv.host}, name: $name")
        JSONObject(text).optString("current", "")
    }
}

suspend fun fetchPcStatus(ctx: Context, srv: Srv): PcStatus = withContext(Dispatchers.IO) {
    val client = CaStore.ensureTrusted(ctx, srv).newBuilder().callTimeout(8, TimeUnit.SECONDS).build()
    client.newCall(Request.Builder().url("https://${srv.host}:${srv.port}/status").build()).execute().use { r ->
        if (!r.isSuccessful) error("获取状态失败 HTTP ${r.code}")
        parseStatus(r.body?.string() ?: "{}")
    }
}

suspend fun switchPcMode(ctx: Context, srv: Srv, mode: StreamMode): String = withContext(Dispatchers.IO) {
    val client = CaStore.ensureTrusted(ctx, srv).newBuilder().callTimeout(8, TimeUnit.SECONDS).build()
    val name = if (mode == StreamMode.Speaker) "speaker" else "mic"
    val body = JSONObject().put("mode", name).toString().toRequestBody("application/json".toMediaType())
    client.newCall(Request.Builder().url("https://${srv.host}:${srv.port}/mode").post(body).build()).execute().use { r ->
        val text = r.body?.string() ?: ""
        if (!r.isSuccessful) error(text.ifEmpty { "切换模式失败 HTTP ${r.code}" })
        Log.i(TAG, "done, 电脑工作模式已切换, host: ${srv.host}, mode: $name")
        JSONObject(text).optString("mode", name)
    }
}