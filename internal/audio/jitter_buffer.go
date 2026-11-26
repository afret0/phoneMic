package audio

import (
	"encoding/binary"
	"sync"
)

const (
	SampleRate        = 48000
	TargetBufferMs    = 100
	MaxBufferMs       = 300
	ReprimeBufferMs   = 30
	FadeMs            = 5
	BytesPerSample    = 2
	targetBufferByte  = SampleRate * BytesPerSample * TargetBufferMs / 1000
	maxBufferByte     = SampleRate * BytesPerSample * MaxBufferMs / 1000
	reprimeBufferByte = SampleRate * BytesPerSample * ReprimeBufferMs / 1000
	fadeSamples       = SampleRate * FadeMs / 1000
)

type JitterBuffer struct {
	mu          sync.Mutex
	buf         []byte
	dropOldest  uint64
	underrun    uint64
	written     uint64
	primed      bool
	reprime     bool
	lastSample  int16
	fadeInLeft  int
	fadeOutLeft int
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
		threshold := targetBufferByte
		if j.reprime {
			threshold = reprimeBufferByte
		}
		if len(j.buf) < threshold {
			j.fillFadeOut(out)
			return true
		}
		j.primed = true
		j.fadeInLeft = fadeSamples
	}

	n := copy(out, j.buf)
	j.buf = append(j.buf[:0], j.buf[n:]...)
	j.applyFadeIn(out[:n])
	if m := n - n%BytesPerSample; m >= BytesPerSample {
		j.lastSample = int16(binary.LittleEndian.Uint16(out[m-BytesPerSample : m]))
	}
	if n < len(out) {
		j.underrun += uint64(len(out) - n)
		j.primed = false
		j.reprime = true
		j.fadeOutLeft = fadeSamples
		j.fillFadeOut(out[n:])
		return true
	}
	return false
}

func (j *JitterBuffer) applyFadeIn(b []byte) {
	samples := len(b) / BytesPerSample
	if j.fadeInLeft < samples {
		samples = j.fadeInLeft
	}
	pos := fadeSamples - j.fadeInLeft
	for i := 0; i < samples; i++ {
		v := int16(binary.LittleEndian.Uint16(b[i*BytesPerSample:]))
		gain := float64(pos+i) / float64(fadeSamples)
		binary.LittleEndian.PutUint16(b[i*BytesPerSample:], uint16(int16(float64(v)*gain)))
	}
	j.fadeInLeft -= samples
}

func (j *JitterBuffer) fillFadeOut(b []byte) {
	i := 0
	for j.fadeOutLeft > 0 && i+BytesPerSample <= len(b) {
		gain := float64(j.fadeOutLeft-1) / float64(fadeSamples)
		v := int16(float64(j.lastSample) * gain)
		binary.LittleEndian.PutUint16(b[i:], uint16(v))
		j.fadeOutLeft--
		i += BytesPerSample
	}
	if j.fadeOutLeft == 0 {
		j.lastSample = 0
	}
	clear(b[i:])
}

func (j *JitterBuffer) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = j.buf[:0]
	j.primed = false
	j.reprime = false
	j.lastSample = 0
	j.fadeInLeft = 0
	j.fadeOutLeft = 0
}

func (j *JitterBuffer) Stats() (bufferedMs int, dropOldest, underrun, written uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.buf) * 1000 / (SampleRate * BytesPerSample), j.dropOldest, j.underrun, j.written
}
