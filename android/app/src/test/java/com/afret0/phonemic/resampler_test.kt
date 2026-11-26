package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ResamplerTest {

    private fun run(srcRate: Int, chunks: List<ShortArray>): List<Short> {
        val r = Resampler(srcRate)
        val out = mutableListOf<Short>()
        for (c in chunks) r.process(c, c.size) { out.add(it) }
        return out
    }

    @Test
    fun downsample96kTakesEverySecondSample() {
        val input = ShortArray(64) { (it * 100).toShort() }
        val out = run(96000, listOf(input))
        assertEquals(32, out.size)
        for (i in out.indices) assertEquals(input[i * 2], out[i])
    }

    @Test
    fun chunkSplitDoesNotChangeOutput() {
        val input = ShortArray(500) { ((it * 37) % 20000 - 10000).toShort() }
        for (srcRate in intArrayOf(44100, 16000, 32000, 96000)) {
            val whole = run(srcRate, listOf(input))
            for (split in intArrayOf(1, 7, 96, 250, 499)) {
                val splitOut = run(srcRate, listOf(input.copyOfRange(0, split), input.copyOfRange(split, input.size)))
                assertEquals("rate=$srcRate split=$split", whole, splitOut)
            }
        }
    }

    @Test
    fun upsample44kFirstSampleAndApproxCount() {
        val input = ShortArray(441) { (it * 10).toShort() }
        val out = run(44100, listOf(input))
        assertEquals(0.toShort(), out.first())
        assertTrue("out=${out.size}", out.size in 479..481)
        for (i in 1 until out.size) assertTrue(out[i] >= out[i - 1])
    }

    @Test
    fun interpolationStaysInRangeAtExtremes() {
        val input = shortArrayOf(32767, -32768, 32767, -32768, 32767, -32768, 32767, -32768)
        val out = run(44100, listOf(input))
        for (v in out) assertTrue(v.toInt() in -32768..32767)
    }

    @Test
    fun identity48kBitExactMinusPendingSample() {
        val input = ShortArray(500) { ((it * 37) % 20000 - 10000).toShort() }
        val out = run(48000, listOf(input))
        assertEquals(input.dropLast(1), out)
    }

    @Test
    fun identity48kChunkSplitInvariant() {
        val input = ShortArray(500) { ((it * 37) % 20000 - 10000).toShort() }
        val whole = run(48000, listOf(input))
        for (split in intArrayOf(1, 7, 96, 250, 499)) {
            val splitOut = run(48000, listOf(input.copyOfRange(0, split), input.copyOfRange(split, input.size)))
            assertEquals("split=$split", whole, splitOut)
        }
    }

    @Test
    fun trimRoundTripToZeroIsBitExact() {
        val input = ShortArray(500) { ((it * 37) % 20000 - 10000).toShort() }
        for (srcRate in intArrayOf(44100, 48000, 96000)) {
            val plain = run(srcRate, listOf(input))
            val r = Resampler(srcRate)
            r.setTrim(500)
            r.setTrim(0)
            val out = mutableListOf<Short>()
            r.process(input, input.size) { out.add(it) }
            assertEquals("rate=$srcRate", plain, out)
        }
    }

    @Test
    fun positiveTrimLowersOutputRate() {
        val input = ShortArray(48000) { ((it * 37) % 20000 - 10000).toShort() }
        val plain = run(48000, listOf(input)).size
        val r = Resampler(48000)
        r.setTrim(500)
        var n = 0
        r.process(input, input.size) { n++ }
        assertTrue("plain=$plain trimmed=$n", n < plain && n >= plain - 30)
    }

    @Test
    fun negativeTrimRaisesOutputRate() {
        val input = ShortArray(48000) { ((it * 37) % 20000 - 10000).toShort() }
        val plain = run(48000, listOf(input)).size
        val r = Resampler(48000)
        r.setTrim(-500)
        var n = 0
        r.process(input, input.size) { n++ }
        assertTrue("plain=$plain trimmed=$n", n > plain && n <= plain + 30)
    }

    @Test
    fun trimClampedAt500() {
        val input = ShortArray(5000) { ((it * 37) % 20000 - 10000).toShort() }
        val a = Resampler(48000)
        a.setTrim(500)
        val b = Resampler(48000)
        b.setTrim(5000)
        val oa = mutableListOf<Short>()
        val ob = mutableListOf<Short>()
        a.process(input, input.size) { oa.add(it) }
        b.process(input, input.size) { ob.add(it) }
        assertEquals(oa, ob)
    }

    @Test
    fun trimSwitchKeepsPhaseAndPrev() {
        val c1 = ShortArray(960) { (it % 1000).toShort() }
        val c2 = ShortArray(960) { ((it + 960) % 1000).toShort() }
        val r = Resampler(48000)
        val out = mutableListOf<Short>()
        r.process(c1, c1.size) { out.add(it) }
        r.setTrim(500)
        r.process(c2, c2.size) { out.add(it) }
        assertEquals(c1.last(), out[959])
    }

    @Test
    fun driftDeadbandBoundary() {
        assertEquals(0, DriftTrim().update(120))
        assertEquals(210, DriftTrim().update(121))
        assertEquals(0, DriftTrim().update(80))
        assertEquals(-210, DriftTrim().update(79))
        assertEquals(0, DriftTrim().update(100))
    }

    @Test
    fun driftClampAt500() {
        assertEquals(500, DriftTrim().update(400))
        assertEquals(-500, DriftTrim().update(-200))
    }

    @Test
    fun driftEmaSmooths() {
        val d = DriftTrim()
        assertEquals(0, d.update(100))
        assertEquals(300, d.update(200))
        assertEquals(210, d.update(100))
    }

    @Test
    fun driftResetClearsEma() {
        val d = DriftTrim()
        d.update(200)
        d.reset()
        assertEquals(0, d.ppm)
        assertEquals(0, d.update(100))
    }
}