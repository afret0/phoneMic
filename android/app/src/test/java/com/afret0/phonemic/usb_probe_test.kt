package com.afret0.phonemic

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UsbProbeTest {
    private val wifi = Srv("客厅电脑", "192.168.1.5", 8443, 8080)
    private val usb = usbServer

    @Test
    fun autoSelectOnlyWhenNothingSelected() {
        assertTrue(shouldAutoSelectUsb(null, usb))
        assertFalse(shouldAutoSelectUsb(wifi, usb))
        assertFalse(shouldAutoSelectUsb(usb, usb))
        assertFalse(shouldAutoSelectUsb(null, null))
    }

    @Test
    fun clearOnlyDeadUsbSelection() {
        assertTrue(shouldClearUsb(usb, false))
        assertFalse(shouldClearUsb(usb, true))
        assertFalse(shouldClearUsb(wifi, false))
        assertFalse(shouldClearUsb(null, false))
        assertFalse(shouldClearUsb(Srv("手动输入", "127.0.0.1", 9443, 8080), false))
    }
}