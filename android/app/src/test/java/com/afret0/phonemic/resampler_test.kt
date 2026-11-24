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
}