package audio

import (
	"encoding/binary"
	"log/slog"
	"math"
	"sync"
	"time"
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

	LowLatTargetBufferMs = 50
	LowLatMaxBufferMs    = 150
	lowLatTargetByte     = SampleRate * BytesPerSample * LowLatTargetBufferMs / 1000
	lowLatMaxByte        = SampleRate * BytesPerSample * LowLatMaxBufferMs / 1000
	driftPeriodSample    = SampleRate * 10 / 1000
	driftWindowSample    = SampleRate * 15 / 1000
	driftFadeSample      = SampleRate * 5 / 1000
	driftPeriodByte      = driftPeriodSample * BytesPerSample
	driftWindowByte      = driftWindowSample * BytesPerSample
	driftFadeByte        = driftFadeSample * BytesPerSample
	driftMinGap          = 10 * time.Second
	driftForceWait       = 60 * time.Second
	driftSilenceRms      = 0.0031623
	driftSilenceEnergy   = driftSilenceRms * driftSilenceRms * driftWindowSample
)

type JitterBuffer struct {
	mu           sync.Mutex
	buf          []byte
	targetByte   int
	maxByte      int
	driftHigh    int
	driftLow     int
	dropOldest   uint64
	underrun     uint64
	written      uint64
	primed       bool
	reprime      bool
	lastSample   int16
	fadeInLeft   int
	fadeOutLeft  int
	lastDropLog  uint64
	driftTrims   uint64
	driftInserts uint64
	driftForced  uint64
	lastAdjust   time.Time
	needSince    time.Time
	needDir      int
	nowFn        func() time.Time
}

func NewJitterBuffer() *JitterBuffer {
	j := &JitterBuffer{nowFn: time.Now}
	j.setProfileLocked(false)
	return j
}

func (j *JitterBuffer) setProfileLocked(lowLat bool) {
	j.targetByte, j.maxByte = targetBufferByte, maxBufferByte
	if lowLat {
		j.targetByte, j.maxByte = lowLatTargetByte, lowLatMaxByte
	}
	j.driftHigh = j.targetByte * 3 / 2
	j.driftLow = j.targetByte * 3 / 5
}

func (j *JitterBuffer) SetProfile(lowLat bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.setProfileLocked(lowLat)
	j.resetLocked()
	slog.Info("hit, 缓冲档位已设置", "lowLat", lowLat,
		"targetMs", j.targetByte*1000/(SampleRate*BytesPerSample),
		"maxMs", j.maxByte*1000/(SampleRate*BytesPerSample),
		"driftHighMs", j.driftHigh*1000/(SampleRate*BytesPerSample),
		"driftLowMs", j.driftLow*1000/(SampleRate*BytesPerSample))
}

func (j *JitterBuffer) TargetMs() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.targetByte * 1000 / (SampleRate * BytesPerSample)
}

func (j *JitterBuffer) MaxMs() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.maxByte * 1000 / (SampleRate * BytesPerSample)
}

func (j *JitterBuffer) Write(pcm []byte) (droppedNow int) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.buf = append(j.buf, pcm...)
	j.written += uint64(len(pcm))

	overflow := len(j.buf) - j.maxByte
	if overflow > 0 {
		overflow += overflow % BytesPerSample
		j.buf = append(j.buf[:0], j.buf[overflow:]...)
		j.dropOldest += uint64(overflow)
		droppedNow = overflow
		return droppedNow
	}
	j.adaptDrift(j.nowFn())
	return 0
}

func (j *JitterBuffer) Read(out []byte) (silenceFilled bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.primed {
		threshold := j.targetByte
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
	j.resetLocked()
}

func (j *JitterBuffer) resetLocked() {
	j.buf = j.buf[:0]
	j.primed = false
	j.reprime = false
	j.lastSample = 0
	j.fadeInLeft = 0
	j.fadeOutLeft = 0
	j.lastAdjust = time.Time{}
	j.needSince = time.Time{}
	j.needDir = 0
}

func (j *JitterBuffer) adaptDrift(now time.Time) {
	dir := 0
	if len(j.buf) > j.driftHigh {
		dir = 1
	} else if j.primed && len(j.buf) < j.driftLow {
		dir = -1
	}
	if dir == 0 {
		j.needSince = time.Time{}
		j.needDir = 0
		return
	}
	if j.needDir != dir {
		j.needDir = dir
		j.needSince = now
	}
	if !j.lastAdjust.IsZero() && now.Sub(j.lastAdjust) < driftMinGap {
		return
	}
	hi := len(j.buf) - driftWindowByte
	if dir < 0 {
		hi -= driftPeriodByte
	}
	mode, at := 0, 0
	if hi >= 0 {
		if off := j.findSilentWindow(hi); off >= 0 {
			mode, at = 1, off
		} else if now.Sub(j.needSince) >= driftForceWait {
			mode, at = 2, j.minEnergyWindow(hi)
		}
	}
	if mode == 0 {
		return
	}
	if dir > 0 {
		j.spliceDrop(at)
		j.driftTrims++
	} else {
		j.spliceInsert(at)
		j.driftInserts++
	}
	if mode == 2 {
		j.driftForced++
	}
	j.lastAdjust = now
	j.needSince = time.Time{}
	j.needDir = 0
	dirName, modeName := "drop", "silence"
	if dir < 0 {
		dirName = "insert"
	}
	if mode == 2 {
		modeName = "forced"
	}
	slog.Info("hit, 时钟漂移自适应拼接",
		"direction", dirName, "mode", modeName,
		"periodMs", driftPeriodSample*1000/SampleRate,
		"bufferedMs", len(j.buf)*1000/(SampleRate*BytesPerSample),
		"driftTrims", j.driftTrims, "driftInserts", j.driftInserts, "driftForced", j.driftForced)
}

func (j *JitterBuffer) sampleAt(byteOff int) float64 {
	return float64(int16(binary.LittleEndian.Uint16(j.buf[byteOff:byteOff+BytesPerSample]))) / 32768
}

func (j *JitterBuffer) windowEnergy(off int) float64 {
	var sum float64
	for i := off; i < off+driftWindowByte; i += BytesPerSample {
		v := j.sampleAt(i)
		sum += v * v
	}
	return sum
}

func (j *JitterBuffer) findSilentWindow(hi int) int {
	sum := j.windowEnergy(0)
	for off := 0; ; off += BytesPerSample {
		if sum < driftSilenceEnergy {
			return off
		}
		if off >= hi {
			return -1
		}
		a := j.sampleAt(off)
		b := j.sampleAt(off + driftWindowByte)
		sum += b*b - a*a
	}
}

func (j *JitterBuffer) minEnergyWindow(hi int) int {
	sum := j.windowEnergy(0)
	best, bestSum := 0, sum
	for off := BytesPerSample; off <= hi; off += BytesPerSample {
		a := j.sampleAt(off - BytesPerSample)
		b := j.sampleAt(off - BytesPerSample + driftWindowByte)
		sum += b*b - a*a
		if sum < bestSum {
			best, bestSum = off, sum
		}
	}
	return best
}

func (j *JitterBuffer) spliceDrop(at int) {
	for i := 0; i < driftFadeByte; i += BytesPerSample {
		t := (float64(i/BytesPerSample) + 1) / (driftFadeSample + 1)
		a := j.sampleAt(at + i)
		b := j.sampleAt(at + driftPeriodByte + i)
		v := int16(math.Round((a*(1-t) + b*t) * 32768))
		binary.LittleEndian.PutUint16(j.buf[at+i:at+i+BytesPerSample], uint16(v))
	}
	copy(j.buf[at+driftFadeByte:], j.buf[at+driftPeriodByte+driftFadeByte:])
	j.buf = j.buf[:len(j.buf)-driftPeriodByte]
}

func (j *JitterBuffer) spliceInsert(off int) {
	s := off + driftFadeByte
	n := len(j.buf)
	seg := make([]byte, driftPeriodByte+2*driftFadeByte)
	for i := 0; i < driftFadeByte; i += BytesPerSample {
		t := (float64(i/BytesPerSample) + 1) / (driftFadeSample + 1)
		a := j.sampleAt(s - driftFadeByte + i)
		b := j.sampleAt(s + i)
		c := j.sampleAt(s + driftPeriodByte + driftFadeByte + i)
		binary.LittleEndian.PutUint16(seg[i:i+BytesPerSample], uint16(int16(math.Round((a*(1-t)+b*t)*32768))))
		binary.LittleEndian.PutUint16(seg[driftFadeByte+driftPeriodByte+i:driftFadeByte+driftPeriodByte+i+BytesPerSample],
			uint16(int16(math.Round((c*(1-t)+b*t)*32768))))
	}
	copy(seg[driftFadeByte:driftFadeByte+driftPeriodByte], j.buf[s+driftFadeByte:s+driftFadeByte+driftPeriodByte])
	j.buf = append(j.buf, make([]byte, driftPeriodByte)...)
	copy(j.buf[s+driftFadeByte+driftPeriodByte:], j.buf[s+driftFadeByte:n])
	copy(j.buf[s-driftFadeByte:], seg)
}

func (j *JitterBuffer) Stats() (bufferedMs int, dropOldest, underrun, written uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.buf) * 1000 / (SampleRate * BytesPerSample), j.dropOldest, j.underrun, j.written
}

func (j *JitterBuffer) DriftStats() (trims, inserts, forced uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.driftTrims, j.driftInserts, j.driftForced
}
