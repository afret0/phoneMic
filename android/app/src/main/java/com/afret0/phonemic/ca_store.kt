package com.afret0.phonemic

import android.content.Context
import android.util.Log
import okhttp3.OkHttpClient
import java.io.ByteArrayInputStream
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

class PinnedCaTrustManager(private val ca: X509Certificate) : X509TrustManager {
    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {}
    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
        val leaf = chain[0]
        leaf.checkValidity()
        var cur = leaf
        for (i in 0 until chain.size) {
            if (cur.issuerX500Principal == ca.subjectX500Principal) {
                cur.verify(ca.publicKey)
                ca.checkValidity()
                return
            }
            val next = chain.getOrNull(i + 1) ?: break
            cur.verify(next.publicKey)
            cur = next
        }
        if (leaf.issuerX500Principal == ca.subjectX500Principal) {
            leaf.verify(ca.publicKey)
            return
        }
        throw java.security.cert.CertificateException("server cert not issued by pinned phoneMic CA")
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf(ca)
}

object CaStore {
    private const val TAG = "CaStore"

    private fun prefs(ctx: Context) = ctx.getSharedPreferences("ca_store", Context.MODE_PRIVATE)

    fun storedCa(ctx: Context, host: String): X509Certificate? {
        val pem = prefs(ctx).getString("ca_" + host, null) ?: return null
        return runCatching { parseCa(pem) }.getOrNull()
    }

    fun parseCa(pem: String): X509Certificate {
        val cf = CertificateFactory.getInstance("X.509")
        return cf.generateCertificate(ByteArrayInputStream(pem.toByteArray())) as X509Certificate
    }

    fun client(ctx: Context, host: String): OkHttpClient {
        val b = OkHttpClient.Builder()
            .connectTimeout(5, TimeUnit.SECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .pingInterval(10, TimeUnit.SECONDS)
        val ca = storedCa(ctx, host)
        if (ca != null) {
            val tm = PinnedCaTrustManager(ca)
            val ssl = SSLContext.getInstance("TLS")
            ssl.init(null, arrayOf(tm), null)
            b.sslSocketFactory(ssl.socketFactory, tm)
        }
        return b.build()
    }

    suspend fun ensureTrusted(ctx: Context, srv: Srv): OkHttpClient {
        val pinned = client(ctx, srv.host)
        val probe = kotlinx.coroutines.Dispatchers.IO
        kotlinx.coroutines.withContext(probe) {
            val caBefore = storedCa(ctx, srv.host)
            val probeResult = runCatching {
                pinned.newCall(okhttp3.Request.Builder().url("https://${srv.host}:${srv.port}/status").build()).execute().use { it.code }
            }
            val ok = probeResult.getOrDefault(-1) in 200..299
            Log.i(TAG, "recv, 信任探测, host: ${srv.host}, 已固定CA: ${caBefore != null}, 探测结果: ${probeResult.getOrElse { it.javaClass.simpleName + ": " + it.message }}")
            if (ok) {
                Log.i(TAG, "hit, 证书已信任, host: ${srv.host}")
                return@withContext pinned
            }
            val exc = probeResult.exceptionOrNull()
            if (caBefore != null && exc != null && exc !is javax.net.ssl.SSLException) {
                Log.i(TAG, "skip, 已固定CA且探测是连接类失败不重取CA, host: ${srv.host}")
                return@withContext pinned
            }
            Log.i(TAG, "recv, 探测未通过走HTTP取CA固定, host: ${srv.host}, httpPort: ${srv.httpPort}")
            val plain = OkHttpClient.Builder().connectTimeout(5, TimeUnit.SECONDS).build()
            val pem = plain.newCall(okhttp3.Request.Builder().url("http://${srv.host}:${srv.httpPort}/ca.crt").build()).execute().use { r ->
                if (!r.isSuccessful) error("ca fetch http ${r.code}")
                r.body?.string() ?: error("ca empty body")
            }
            val ca = parseCa(pem)
            val tm = PinnedCaTrustManager(ca)
            val ssl = SSLContext.getInstance("TLS")
            ssl.init(null, arrayOf(tm), null)
            val tofu = pinned.newBuilder().sslSocketFactory(ssl.socketFactory, tm).build()
            tofu.newCall(okhttp3.Request.Builder().url("https://${srv.host}:${srv.port}/status").build()).execute().use {
                if (!it.isSuccessful) error("status http ${it.code}")
            }
            prefs(ctx).edit().putString("ca_" + srv.host, pem).apply()
            Log.i(TAG, "done, CA 已固定, host: ${srv.host}, subject: ${ca.subjectX500Principal.name}")
            tofu
        }
        return client(ctx, srv.host)
    }
}