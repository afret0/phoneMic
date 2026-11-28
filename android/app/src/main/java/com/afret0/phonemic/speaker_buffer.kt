package com.afret0.phonemic

class SpeakerBuffer(
    private val targetBytes: Int = 9600,
    private val maxBytes: Int = 28800,
) {
    private val chunks = ArrayDeque<ByteArray>()
    private var headOff = 0
    private var total = 0
    private var primed = false

    var droppedBytes = 0L
        private set
    var underrunBytes = 0L
        private set

    @Synchronized
    fun push(b: ByteArray) {
        if (b.isEmpty()) return
        chunks.addLast(b)
        total += b.size
        var overflow = total - maxBytes
        if (overflow <= 0) return
        overflow += overflow % 2
        var left = overflow
        while (left > 0 && chunks.isNotEmpty()) {
            val head = chunks.first()
            val avail = head.size - headOff
            if (avail > left) {
                headOff += left
                total -= left
                left = 0
            } else {
                chunks.removeFirst()
                headOff = 0
                total -= avail
                left -= avail
            }
        }
        droppedBytes += overflow
    }

    @Synchronized
    fun read(out: ShortArray): Int {
        val wantBytes = out.size * 2
        if (!primed) {
            if (total < targetBytes) return 0
            primed = true
        }
        var bytePos = 0
        while (bytePos < wantBytes && chunks.isNotEmpty()) {
            val head = chunks.first()
            val n = minOf(head.size - headOff, wantBytes - bytePos)
            var i = 0
            while (i + 1 < n) {
                val lo = head[headOff + i].toInt() and 0xFF
                val hi = head[headOff + i + 1].toInt()
                out[(bytePos + i) / 2] = ((hi shl 8) or lo).toShort()
                i += 2
            }
            headOff += n
            bytePos += n
            total -= n
            if (headOff >= head.size) {
                chunks.removeFirst()
                headOff = 0
            }
        }
        val got = bytePos / 2
        if (bytePos < wantBytes) {
            underrunBytes += (wantBytes - bytePos).toLong()
            primed = false
            for (i in got until out.size) out[i] = 0
        }
        return got
    }

    @Synchronized
    fun bufferedMs(): Int = total / 96

    @Synchronized
    fun reset() {
        chunks.clear()
        headOff = 0
        total = 0
        primed = false
    }
}