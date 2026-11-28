package com.afret0.phonemic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class SpeakerBufferTest {
    private fun pcmBytes(nSamples: Int, start: Int = 0): ByteArray {
        val b = ByteArray(nSamples * 2)
        for (i in 0 until nSamples) {
            val v = start + i
            b[i * 2] = (v and 0xFF).toByte()
            b[i * 2 + 1] = ((v shr 8) and 0xFF).toByte()
        }
        return b
    }

    @Test
    fun primeThresholdExactly100ms() {
        val buf = SpeakerBuffer()
        val out = ShortArray(480)
        buf.push(pcmBytes(4799))
        assertEquals(0, buf.read(out))
        assertEquals(99, buf.bufferedMs())
        buf.push(pcmBytes(1, 9000))
        assertEquals(480, buf.read(out))
        assertEquals(0, out[0].toInt())
        assertEquals(478, out[478].toInt())
        assertEquals(479, out[479].toInt())
        val rest = ShortArray(4320)
        assertEquals(4320, buf.read(rest))
        assertEquals(9000, rest[4319].toInt())
    }

    @Test
    fun overflowDropsOldestExactlyAtMax() {
        val buf = SpeakerBuffer()
        buf.push(pcmBytes(14400))
        assertEquals(0L, buf.droppedBytes)
        assertEquals(300, buf.bufferedMs())
        buf.push(pcmBytes(96, 20000))
        assertEquals(192L, buf.droppedBytes)
        assertEquals(300, buf.bufferedMs())
        val out = ShortArray(14400)
        assertEquals(14400, buf.read(out))
        assertEquals(96, out[0].toInt())
        assertEquals(20095, out[14399].toInt())
    }

    @Test
    fun oddOverflowTruncatedToEven() {
        val buf = SpeakerBuffer(targetBytes = 100, maxBytes = 1001)
        buf.push(pcmBytes(500))
        buf.push(pcmBytes(1, 777))
        assertEquals(2L, buf.droppedBytes)
        assertEquals(1000 / 96, buf.bufferedMs())
        val out = ShortArray(500)
        assertEquals(500, buf.read(out))
        assertEquals(1, out[0].toInt())
        assertEquals(777, out[499].toInt())
    }

    @Test
    fun partialHeadSurvivesOverflow() {
        val buf = SpeakerBuffer(targetBytes = 1000, maxBytes = 2000)
        buf.push(pcmBytes(800))
        buf.push(pcmBytes(800, 800))
        assertEquals(1200L, buf.droppedBytes)
        val out = ShortArray(1000)
        assertEquals(1000, buf.read(out))
        assertEquals(600, out[0].toInt())
        assertEquals(1599, out[999].toInt())
    }

    @Test
    fun underrunReturnsPartialAndReprimes() {
        val buf = SpeakerBuffer()
        assertEquals(0, buf.read(ShortArray(480)))
        assertEquals(0L, buf.underrunBytes)
        buf.push(pcmBytes(4800))
        repeat(10) { assertEquals(480, buf.read(ShortArray(480))) }
        buf.push(pcmBytes(96, 500))
        val partial = ShortArray(480)
        assertEquals(96, buf.read(partial))
        assertEquals(500, partial[0].toInt())
        assertEquals(0, partial[96].toInt())
        assertEquals(768L, buf.underrunBytes)
        buf.push(pcmBytes(96, 900))
        assertEquals(0, buf.read(ShortArray(480)))
        buf.push(pcmBytes(4704, 1000))
        assertEquals(480, buf.read(ShortArray(480)))
    }

    @Test
    fun anyChunkingSameStreamOutput() {
        val total = 12000
        val stream = pcmBytes(total)
        val whole = SpeakerBuffer()
        whole.push(stream)
        val outWhole = ShortArray(total)
        assertEquals(total, whole.read(outWhole))

        val split = SpeakerBuffer()
        var pos = 0
        val sizes = intArrayOf(1920, 2, 4800, 9600, 960, 6)
        var si = 0
        while (pos < stream.size) {
            val n = minOf(sizes[si % sizes.size], stream.size - pos) and 0xFFFE
            val take = if (n == 0) stream.size - pos else n
            split.push(stream.copyOfRange(pos, pos + take))
            pos += take
            si++
        }
        val outSplit = ShortArray(total)
        assertEquals(total, split.read(outSplit))
        assertTrue(outWhole.contentEquals(outSplit))
        assertEquals(whole.droppedBytes, split.droppedBytes)
        assertEquals(0L, split.underrunBytes)
    }
}