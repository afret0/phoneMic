package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class DeviceNameTest {
    @Test
    fun settingsNameWinsOverBuild() {
        assertEquals("小明的小米", pickDeviceName("小明的小米", "Xiaomi", "23127PN0CC", "Phone-AB2C"))
    }

    @Test
    fun settingsNameTrimmedAndCapped() {
        assertEquals("我的手机", pickDeviceName("  我的手机  ", null, null, null))
        val long = "a".repeat(80)
        assertEquals("a".repeat(64), pickDeviceName(long, "Xiaomi", "13", null))
    }

    @Test
    fun blankSettingsFallsBackToBuild() {
        assertEquals("Xiaomi 23127PN0CC", pickDeviceName("   ", "Xiaomi", "23127PN0CC", null))
        assertEquals("google Pixel 7", pickDeviceName(null, "google", "Pixel 7", "Phone-AB2C"))
    }

    @Test
    fun savedRandomUsedWhenBuildMissing() {
        assertEquals("Phone-AB2C", pickDeviceName(null, "", " ", "Phone-AB2C"))
        assertEquals("", pickDeviceName(null, null, null, "  "))
        assertEquals("", pickDeviceName("", "", "", null))
    }

    @Test
    fun buildNameSkipsDuplicateManufacturer() {
        assertEquals("google Pixel 7", buildDeviceName("Google", "google Pixel 7"))
        assertEquals("Xiaomi 23127PN0CC", buildDeviceName("Xiaomi", "23127PN0CC"))
        assertEquals("23127PN0CC", buildDeviceName("", "23127PN0CC"))
        assertEquals("Xiaomi", buildDeviceName("Xiaomi", ""))
        assertEquals("", buildDeviceName(null, null))
    }

    @Test
    fun randomNameShape() {
        val pat = Regex("^Phone-[A-HJ-KM-NP-Z2-9]{4}$")
        repeat(20) {
            assertTrue(randomPhoneName().matches(pat))
        }
    }
}