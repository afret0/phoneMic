package com.afret0.phonemic

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.provider.Settings
import android.util.Log
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.io.File
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlin.coroutines.coroutineContext

data class AppUpdate(val tag: String, val version: String, val apkUrl: String, val size: Long, val sha256: String)

object UpdateChecker {
    private const val TAG = "Update"
    const val LATEST_API = "https://api.github.com/repos/afret0/phoneMic/releases/latest"
    const val RELEASE_PAGE = "https://github.com/afret0/phoneMic/releases/latest"
    private const val SKIP_KEY = "update_skip_tag"
    private const val APK_SUFFIX = "_android_debug.apk"

    private val client = OkHttpClient.Builder()
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .build()

    fun parseVersion(s: String): List<Int>? {
        if (s.isEmpty()) return null
        val parts = s.split(".")
        val nums = ArrayList<Int>(parts.size)
        for (p in parts) {
            val n = p.toIntOrNull() ?: return null
            if (n < 0) return null
            nums.add(n)
        }
        return nums
    }

    fun isNewer(candidate: String, current: String): Boolean {
        val a = parseVersion(candidate) ?: return false
        val b = parseVersion(current) ?: return false
        for (i in 0 until maxOf(a.size, b.size)) {
            val va = a.getOrElse(i) { 0 }
            val vb = b.getOrElse(i) { 0 }
            if (va != vb) return va > vb
        }
        return false
    }

    fun parseRelease(json: String, current: String): AppUpdate? {
        val rel = JSONObject(json)
        val tag = rel.optString("tag_name")
        val ver = tag.removePrefix("v")
        if (parseVersion(ver) == null || !isNewer(ver, current)) return null
        val assets = rel.optJSONArray("assets") ?: return null
        val want = "phoneMic_" + ver + APK_SUFFIX
        var pick: JSONObject? = null
        for (i in 0 until assets.length()) {
            val a = assets.getJSONObject(i)
            val name = a.optString("name")
            if (name == want) {
                pick = a
                break
            }
            if (pick == null && name.endsWith(APK_SUFFIX)) pick = a
        }
        val a = pick ?: return null
        val url = a.optString("browser_download_url")
        if (url.isEmpty()) return null
        val digest = a.optString("digest")
        val sha = if (digest.startsWith("sha256:")) digest.removePrefix("sha256:") else ""
        return AppUpdate(tag, ver, url, a.optLong("size"), sha)
    }

    suspend fun check(current: String): AppUpdate? = withContext(Dispatchers.IO) {
        val req = Request.Builder().url(LATEST_API)
            .header("Accept", "application/vnd.github+json")
            .header("User-Agent", "phoneMic/$current")
            .build()
        client.newCall(req).execute().use { r ->
            if (r.code == 404) {
                Log.i(TAG, "skip, 仓库尚无 release, 视为已是最新")
                return@use null
            }
            if (!r.isSuccessful) error("release api http ${r.code}")
            val body = r.body?.string() ?: error("release api 空响应")
            val u = parseRelease(body, current)
            Log.i(TAG, "done, 版本检查完成, 当前: $current,  newer: ${u != null}, latest: ${u?.tag}")
            u
        }
    }

    private fun prefs(ctx: Context) = ctx.getSharedPreferences("phone_mic", Context.MODE_PRIVATE)

    fun skippedTag(ctx: Context): String? = prefs(ctx).getString(SKIP_KEY, null)

    fun skipVersion(ctx: Context, tag: String) {
        prefs(ctx).edit().putString(SKIP_KEY, tag).apply()
        Log.i(TAG, "done, 已跳过版本, tag: $tag")
    }

    fun canInstallPackages(ctx: Context): Boolean = ctx.packageManager.canRequestPackageInstalls()

    fun openInstallPermission(ctx: Context) {
        Log.i(TAG, "recv, 引导开启安装未知应用权限")
        ctx.startActivity(
            Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + ctx.packageName))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
    }

    suspend fun download(ctx: Context, u: AppUpdate, onProgress: (Long, Long) -> Unit): File =
        withContext(Dispatchers.IO) {
            val dir = ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: error("外部文件目录不可用")
            dir.mkdirs()
            val out = File(dir, "phoneMic_${u.version}$APK_SUFFIX")
            if (out.exists() && out.length() == u.size && (u.sha256.isEmpty() || fileSha256(out).equals(u.sha256, true))) {
                Log.i(TAG, "hit, 安装包已存在且校验一致, 复用, size: ${out.length()}")
                return@withContext out
            }
            val tmp = File(dir, out.name + ".part")
            try {
                client.newCall(Request.Builder().url(u.apkUrl).build()).execute().use { r ->
                    if (!r.isSuccessful) error("apk download http ${r.code}")
                    val src = r.body?.byteStream() ?: error("apk 空响应")
                    val md = MessageDigest.getInstance("SHA-256")
                    var total = 0L
                    tmp.outputStream().use { sink ->
                        val buf = ByteArray(64 * 1024)
                        while (true) {
                            coroutineContext.ensureActive()
                            val n = src.read(buf)
                            if (n < 0) break
                            sink.write(buf, 0, n)
                            md.update(buf, 0, n)
                            total += n
                            onProgress(total, u.size)
                        }
                    }
                    if (u.size > 0 && total != u.size) error("下载字节数不符: 期望 ${u.size}, 实际 $total")
                    if (u.sha256.isNotEmpty() && !md.digest().toHex().equals(u.sha256, true)) error("sha256 校验失败")
                }
                if (!tmp.renameTo(out)) error("下载文件重命名失败")
                Log.i(TAG, "done, 安装包下载完成, version: ${u.version}, size: ${out.length()}")
                out
            } catch (e: Exception) {
                tmp.delete()
                throw e
            }
        }

    fun installApk(ctx: Context, f: File) {
        val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".fileprovider", f)
        ctx.startActivity(
            Intent(Intent.ACTION_VIEW)
                .setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK),
        )
        Log.i(TAG, "done, 已拉起系统安装器, file: ${f.name}")
    }

    private fun fileSha256(f: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        f.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buf)
                if (n < 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().toHex()
    }

    private fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it) }
}