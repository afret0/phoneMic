package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DeviceApiTest {
    private val cable = PcDevice("CABLE Input (VB-Audio Virtual Cable)", true)
    private val speaker = PcDevice("扬声器 (Realtek Audio)", false)

    @Test
    fun `未加载列表选中自动`() {
        assertEquals("", pickedDeviceName(null))
    }

    @Test
    fun `自动模式选中自动`() {
        assertEquals("", pickedDeviceName(PcDeviceList(listOf(cable, speaker), auto = true, current = "扬声器 (Realtek Audio)")))
    }

    @Test
    fun `current 为空选中自动`() {
        assertEquals("", pickedDeviceName(PcDeviceList(listOf(cable), auto = false, current = "")))
    }

    @Test
    fun `手动模式选中当前设备`() {
        assertEquals("扬声器 (Realtek Audio)", pickedDeviceName(PcDeviceList(listOf(cable, speaker), auto = false, current = "扬声器 (Realtek Audio)")))
    }

    @Test
    fun `虚拟声卡识别不区分大小写`() {
        assertTrue(isVirtualDevice("CABLE Input (VB-Audio Virtual Cable)"))
        assertTrue(isVirtualDevice("BlackHole 2ch"))
        assertTrue(isVirtualDevice("blackhole 16ch"))
    }

    @Test
    fun `物理设备不判为虚拟声卡`() {
        assertFalse(isVirtualDevice("扬声器 (Realtek Audio)"))
        assertFalse(isVirtualDevice("Speakers"))
        assertFalse(isVirtualDevice(""))
    }
}