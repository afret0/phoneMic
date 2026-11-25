package com.afret0.phonemic

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FollowUpdateTest {
    private val base = Srv("phoneMic-PC", "192.168.31.126", 8443, 8080)

    @Test
    fun `无选中项不跟随`() {
        assertFalse(shouldFollowSelected(null, base))
    }

    @Test
    fun `同名同地址不重复更新`() {
        assertFalse(shouldFollowSelected(base, base.copy()))
    }

    @Test
    fun `同名换 IP 跟随`() {
        assertTrue(shouldFollowSelected(base.copy(host = "172.17.0.1"), base))
    }

    @Test
    fun `同名换端口跟随`() {
        assertTrue(shouldFollowSelected(base.copy(port = 9443), base))
    }

    @Test
    fun `同名换 http 端口跟随`() {
        assertTrue(shouldFollowSelected(base.copy(httpPort = 9080), base))
    }

    @Test
    fun `扫码按 IP 选中不被发现覆盖`() {
        val scanned = Srv("192.168.31.126", "192.168.31.126", 8443, 8080)
        assertFalse(shouldFollowSelected(scanned, base))
    }
}