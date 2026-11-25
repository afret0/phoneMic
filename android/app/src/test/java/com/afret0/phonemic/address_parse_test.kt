package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class AddressParseTest {
    private val known = listOf(Srv("phoneMic-DESKTOP", "192.168.31.126", 8443, 9080))

    @Test
    fun scannedUrlYieldsHostAndPort() {
        val s = parseManual("https://192.168.31.126:8443", emptyList())!!
        assertEquals("192.168.31.126", s.host)
        assertEquals(8443, s.port)
        assertEquals(8080, s.httpPort)
        assertEquals("手动输入", s.name)
    }

    @Test
    fun scannedUrlKeepsKnownServerIdentity() {
        val s = parseManual("https://192.168.31.126:8443", known)!!
        assertEquals("phoneMic-DESKTOP", s.name)
        assertEquals(9080, s.httpPort)
    }

    @Test
    fun hostOnlyDefaultsTo8443() {
        val s = parseManual("192.168.31.9", emptyList())!!
        assertEquals("192.168.31.9", s.host)
        assertEquals(8443, s.port)
    }

    @Test
    fun trailingSlashAndSpacesTolerated() {
        val s = parseManual("  https://192.168.31.126:8443/  ", emptyList())!!
        assertEquals("192.168.31.126", s.host)
        assertEquals(8443, s.port)
    }

    @Test
    fun garbageQrContentRejected() {
        assertNull(parseManual("WIFI:S=MyWiFi;T=WPA;P=secret;;", emptyList()))
        assertNull(parseManual("", emptyList()))
        assertNull(parseManual("https://", emptyList()))
    }
}