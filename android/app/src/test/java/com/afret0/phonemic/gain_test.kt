package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class GainTest {
    private fun process(v: Float, gain: Float): Float =
        applyGainSoft((v * 32768f).toInt().coerceIn(-32768, 32767).toShort(), gain, SoftLimiter()) / 32768f

    @Test
    fun `滑杆端点与中点映射到倍数`() {
        assertEquals(1f, gainFromSlider(0f), 1e-4f)
        assertEquals(10f, gainFromSlider(1f), 1e-3f)
        assertEquals(3.1623f, gainFromSlider(0.5f), 1e-3f)
        assertEquals(1f, gainFromSlider(-0.5f), 1e-4f)
        assertEquals(10f, gainFromSlider(1.5f), 1e-3f)
    }

    @Test
    fun `滑杆与倍数互转往返一致`() {
        for (g in listOf(1f, 1.7f, 2.5f, 5f, 7.3f, 10f)) {
            assertEquals(g, gainFromSlider(sliderFromGain(g)), 1e-3f)
        }
        assertEquals(0f, sliderFromGain(0.5f), 1e-6f)
        assertEquals(1f, sliderFromGain(20f), 1e-6f)
    }

    @Test
    fun `一倍增益逐位不变`() {
        for (s in listOf(0, 1, -1, 1234, -1234, 32767, -32768)) {
            assertEquals(s.toShort(), applyGainSoft(s.toShort(), 1f, SoftLimiter()))
        }
    }

    @Test
    fun `膝点以下按倍数线性放大且对称`() {
        assertEquals(0.5f, process(0.1f, 5f), 2e-4f)
        assertEquals(-0.5f, process(-0.1f, 5f), 2e-4f)
        assertEquals(0.2f, process(0.2f, 1f), 2e-4f)
    }

    @Test
    fun `过载被软限幅在满幅以内`() {
        val out = process(0.2f, 5f)
        assertTrue("软限幅须压回满幅以内, out: $out", out < 1f)
        assertTrue("软限幅须保留高电平, out: $out", out > 0.9f)
        assertEquals(-out, process(-0.2f, 5f), 4e-5f)
        val extremePos = process(0.9f, 10f)
        val extremeNeg = process(-0.9f, 10f)
        assertTrue("极端过载不得越过正满幅, out: $extremePos", extremePos <= 1f && extremePos > 0.9f)
        assertTrue("极端过载不得越过负满幅, out: $extremeNeg", extremeNeg >= -1f && extremeNeg < -0.9f)
    }

    @Test
    fun `输出随输入单调不减`() {
        var last = -1f
        for (i in -100..100) {
            val out = process(i / 100f, 10f)
            assertTrue("单调性破坏, i: $i, out: $out, last: $last", out >= last)
            last = out
        }
    }

    @Test
    fun `倍数文案格式`() {
        assertEquals("1.0x", fmtGain(1f))
        assertEquals("2.5x", fmtGain(2.5f))
        assertEquals("9.9x", fmtGain(9.9f))
        assertEquals("10x", fmtGain(10f))
    }
}