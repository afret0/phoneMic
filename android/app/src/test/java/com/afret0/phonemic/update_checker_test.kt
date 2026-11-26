package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateCheckerTest {
    @Test
    fun parseVersion_valid() {
        assertEquals(listOf(0, 1, 20), UpdateChecker.parseVersion("0.1.20"))
        assertEquals(listOf(1, 0), UpdateChecker.parseVersion("1.0"))
        assertEquals(listOf(7), UpdateChecker.parseVersion("7"))
    }

    @Test
    fun parseVersion_invalid() {
        assertNull(UpdateChecker.parseVersion(""))
        assertNull(UpdateChecker.parseVersion("0..1"))
        assertNull(UpdateChecker.parseVersion("a.b"))
        assertNull(UpdateChecker.parseVersion("1.x"))
        assertNull(UpdateChecker.parseVersion("-1.2"))
        assertNull(UpdateChecker.parseVersion("1.2."))
    }

    @Test
    fun isNewer_table() {
        assertTrue(UpdateChecker.isNewer("0.1.21", "0.1.20"))
        assertFalse(UpdateChecker.isNewer("0.1.20", "0.1.21"))
        assertFalse(UpdateChecker.isNewer("0.1.20", "0.1.20"))
        assertTrue(UpdateChecker.isNewer("0.2.0", "0.1.99"))
        assertTrue(UpdateChecker.isNewer("1.0.0", "0.99.99"))
        assertTrue(UpdateChecker.isNewer("0.1.100", "0.1.99"))
        assertFalse(UpdateChecker.isNewer("0.2", "0.2.0"))
        assertTrue(UpdateChecker.isNewer("0.1.1", "0.1"))
        assertFalse(UpdateChecker.isNewer("bad", "0.1.0"))
        assertFalse(UpdateChecker.isNewer("0.2.0", "bad"))
    }

    private fun releaseJson(tag: String, assetName: String, url: String, size: Long, digest: String): String =
        """{"tag_name":"$tag","assets":[{"name":"$assetName","browser_download_url":"$url","size":$size,"digest":"$digest"}]}"""

    @Test
    fun parseRelease_newerWithExactAsset() {
        val json = releaseJson("v0.2.0", "phoneMic_0.2.0_android_debug.apk", "https://x/apk", 123, "sha256:abcd")
        val u = UpdateChecker.parseRelease(json, "0.1.20")!!
        assertEquals("v0.2.0", u.tag)
        assertEquals("0.2.0", u.version)
        assertEquals("https://x/apk", u.apkUrl)
        assertEquals(123L, u.size)
        assertEquals("abcd", u.sha256)
    }

    @Test
    fun parseRelease_sameVersionIsNull() {
        val json = releaseJson("v0.1.20", "phoneMic_0.1.20_android_debug.apk", "https://x/apk", 1, "")
        assertNull(UpdateChecker.parseRelease(json, "0.1.20"))
    }

    @Test
    fun parseRelease_badTagIsNull() {
        val json = releaseJson("nightly", "phoneMic_0.2.0_android_debug.apk", "https://x/apk", 1, "")
        assertNull(UpdateChecker.parseRelease(json, "0.1.20"))
    }

    @Test
    fun parseRelease_noApkAssetIsNull() {
        val json = releaseJson("v0.2.0", "phoneMic_0.2.0_win_x64.exe", "https://x/exe", 1, "")
        assertNull(UpdateChecker.parseRelease(json, "0.1.20"))
    }

    @Test
    fun parseRelease_suffixFallbackAndExactWins() {
        val fallback = """{"tag_name":"v0.2.0","assets":[
            {"name":"phoneMic_0.1.0_android_debug.apk","browser_download_url":"https://x/old","size":1},
            {"name":"phoneMic_0.2.0_android_debug.apk","browser_download_url":"https://x/new","size":2}]}"""
        assertEquals("https://x/new", UpdateChecker.parseRelease(fallback, "0.1.20")!!.apkUrl)
        val onlyOld = """{"tag_name":"v0.2.0","assets":[
            {"name":"phoneMic_0.1.0_android_debug.apk","browser_download_url":"https://x/old","size":1}]}"""
        assertEquals("https://x/old", UpdateChecker.parseRelease(onlyOld, "0.1.20")!!.apkUrl)
    }

    @Test
    fun parseRelease_nonSha256DigestIgnored() {
        val json = releaseJson("v0.2.0", "phoneMic_0.2.0_android_debug.apk", "https://x/apk", 5, "md5:zz")
        assertEquals("", UpdateChecker.parseRelease(json, "0.1.20")!!.sha256)
    }
}