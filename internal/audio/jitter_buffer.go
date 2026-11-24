package audio

import "sync"

const (
	SampleRate       = 48000
	TargetBufferMs   = 100
	MaxBufferMs      = 300
	BytesPerSample   = 2
	targetBufferByte = SampleRate * BytesPerSample * TargetBufferMs / 1000
	maxBufferByte    = SampleRate * BytesPerSample * MaxBufferMs / 1000
)

type JitterBuffer struct {
	mu          sync.Mutex
	buf         []byte
	dropOldest  uint64
	underrun    uint64
	written     uint64
	primed      bool
	lastDropLog uint64
}

func NewJitterBuffer() *JitterBuffer {
	return &JitterBuffer{}
}

func (j *JitterBuffer) Write(pcm []byte) (droppedNow int) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.buf = append(j.buf, pcm...)
	j.written += uint64(len(pcm))

	overflow := len(j.buf) - maxBufferByte
	if overflow > 0 {
		overflow += overflow % BytesPerSample
		j.buf = append(j.buf[:0], j.buf[overflow:]...)
		j.dropOldest += uint64(overflow)
		droppedNow = overflow
	}
	return droppedNow
}

func (j *JitterBuffer) Read(out []byte) (silenceFilled bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.primed {
		if len(j.buf) < targetBufferByte {
			clear(out)
			return true
		}
		j.primed = true
	}

	n := copy(out, j.buf)
	j.buf = append(j.buf[:0], j.buf[n:]...)
	if n < len(out) {
		clear(out[n:])
		j.underrun += uint64(len(out) - n)
		j.primed = false
		return true
	}
	return false
}

func (j *JitterBuffer) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = j.buf[:0]
	j.primed = false
}

func (j *JitterBuffer) Stats() (bufferedMs int, dropOldest, underrun, written uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.buf) * 1000 / (SampleRate * BytesPerSample), j.dropOldest, j.underrun, j.written
}
