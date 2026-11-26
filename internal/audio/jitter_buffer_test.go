package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func pcmSeq(startSample uint16, samples int) []byte {
	b := make([]byte, samples*BytesPerSample)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(b[i*2:], startSample+uint16(i))
	}
	return b
}

func targetSamples() int { return targetBufferByte / BytesPerSample }
func maxSamples() int    { return maxBufferByte / BytesPerSample }

func TestJitterBufferPriming(t *testing.T) {
	cases := []struct {
		name        string
		writeBytes  int
		wantSilence bool
	}{
		{"刚好达标", targetBufferByte, false},
		{"差一个采样不达标", targetBufferByte - BytesPerSample, true},
		{"空缓冲", 0, true},
		{"半目标量", targetBufferByte / 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := NewJitterBuffer()
			if c.writeBytes > 0 {
				j.Write(pcmSeq(1, c.writeBytes/BytesPerSample))
			}
			out := make([]byte, 960)
			silence := j.Read(out)
			if silence != c.wantSilence {
				t.Fatalf("silence = %t, want %t (writeBytes: %d, target: %d)", silence, c.wantSilence, c.writeBytes, targetBufferByte)
			}
			if c.wantSilence && !bytes.Equal(out, make([]byte, 960)) {
				t.Fatalf("未达标时输出必须全零")
			}
			if !c.wantSilence {
				if binary.LittleEndian.Uint16(out) != 0 {
					t.Fatalf("淡入首采样必须为 0, 实际: %d", int16(binary.LittleEndian.Uint16(out)))
				}
				got := binary.LittleEndian.Uint16(out[fadeSamples*BytesPerSample:])
				if got != uint16(1+fadeSamples) {
					t.Fatalf("淡入结束后应输出原始数据, 采样序号: %d, want %d", got, 1+fadeSamples)
				}
			}
		})
	}
}

func TestJitterBufferOverflowDropOldest(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(pcmSeq(0, maxSamples()))
	dropped := j.Write(pcmSeq(1000, 2400))
	if dropped != 4800 {
		t.Fatalf("dropped: %d, want 4800", dropped)
	}
	bufferedMs, dropTotal, _, written := j.Stats()
	if dropTotal != 4800 || bufferedMs != MaxBufferMs {
		t.Fatalf("dropTotal: %d, bufferedMs: %d, want 4800 / %d", dropTotal, bufferedMs, MaxBufferMs)
	}
	if written != uint64(maxBufferByte+4800) {
		t.Fatalf("written: %d", written)
	}
	j.mu.Lock()
	first := binary.LittleEndian.Uint16(j.buf)
	j.mu.Unlock()
	if first != 2400 {
		t.Fatalf("丢的必须是最旧数据, 队头采样序号: %d, want 2400", first)
	}
}

func TestJitterBufferOverflowOddRoundedToSample(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(bytes.Repeat([]byte{0x20, 0x4E}, maxSamples()))
	dropped := j.Write([]byte{1, 2, 3})
	if dropped != 4 {
		t.Fatalf("奇数溢出应取偶丢整采样, dropped: %d, want 4", dropped)
	}
	bufferedMs, dropTotal, _, _ := j.Stats()
	if dropTotal != 4 || bufferedMs != (maxBufferByte+3-4)*1000/(SampleRate*BytesPerSample) {
		t.Fatalf("dropTotal: %d, bufferedMs: %d", dropTotal, bufferedMs)
	}
}

func TestJitterBufferUnderrunPartialFill(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(pcmSeq(7, targetSamples()))
	out := make([]byte, targetBufferByte*2)
	silence := j.Read(out)
	if !silence {
		t.Fatalf("数据不足应报 silenceFilled")
	}
	if binary.LittleEndian.Uint16(out) != 0 {
		t.Fatalf("淡入首采样必须为 0, 实际: %d", int16(binary.LittleEndian.Uint16(out)))
	}
	if got := binary.LittleEndian.Uint16(out[fadeSamples*BytesPerSample:]); got != uint16(7+fadeSamples) {
		t.Fatalf("已有数据应先输出(淡入区外无衰减), 采样序号: %d, want %d", got, 7+fadeSamples)
	}
	if !bytes.Equal(out[targetBufferByte+fadeSamples*BytesPerSample:], make([]byte, targetBufferByte-fadeSamples*BytesPerSample)) {
		t.Fatalf("淡出结束后缺口必须补零")
	}
	_, _, underrun, _ := j.Stats()
	if underrun != uint64(targetBufferByte) {
		t.Fatalf("underrun: %d, want %d", underrun, targetBufferByte)
	}
}

func TestJitterBufferReprimeThreshold(t *testing.T) {
	cases := []struct {
		name        string
		writeBytes  int
		wantSilence bool
	}{
		{"刚好达标", reprimeBufferByte, false},
		{"差一个采样不达标", reprimeBufferByte - BytesPerSample, true},
		{"空缓冲", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := NewJitterBuffer()
			j.Write(pcmSeq(1, targetSamples()))
			j.Read(make([]byte, targetBufferByte*2))
			if c.writeBytes > 0 {
				j.Write(pcmSeq(1, c.writeBytes/BytesPerSample))
			}
			silence := j.Read(make([]byte, 960))
			if silence != c.wantSilence {
				t.Fatalf("silence = %t, want %t (writeBytes: %d, reprime: %d)", silence, c.wantSilence, c.writeBytes, reprimeBufferByte)
			}
		})
	}
}

func TestJitterBufferReprimeOnlyAfterUnderrun(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(pcmSeq(1, targetSamples()))
	j.Read(make([]byte, targetBufferByte*2))
	j.Reset()
	j.Write(pcmSeq(1, reprimeBufferByte/BytesPerSample))
	if !j.Read(make([]byte, 960)) {
		t.Fatalf("Reset 后首次攒量仍须满 %dms, %dms 不应出声", TargetBufferMs, ReprimeBufferMs)
	}
}

func TestJitterBufferFadeOutDecay(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(bytes.Repeat([]byte{0x20, 0x4E}, targetSamples()))
	j.Read(make([]byte, 960))
	realLeft := targetSamples() - 480
	out := make([]byte, (realLeft+200)*BytesPerSample)
	if !j.Read(out) {
		t.Fatalf("数据不足应报 silenceFilled")
	}
	gap := out[realLeft*BytesPerSample:]
	first := int16(binary.LittleEndian.Uint16(gap))
	if math.Abs(float64(first)-20000) > 20000.0/float64(fadeSamples)+1 {
		t.Fatalf("淡出首采样必须贴近末真实采样 20000, 实际: %d", first)
	}
	prev := int16(20000)
	for i := 0; i < 200; i++ {
		v := int16(binary.LittleEndian.Uint16(gap[i*BytesPerSample:]))
		if v > prev || v < 0 {
			t.Fatalf("淡出必须单调衰减到 0, 第 %d 采样: %d, 前值: %d", i, v, prev)
		}
		prev = v
	}
	sil := make([]byte, 960)
	if !j.Read(sil) {
		t.Fatalf("未攒够恢复阈值应继续静音")
	}
	border := int16(binary.LittleEndian.Uint16(sil))
	if math.Abs(float64(border)-float64(prev)) > 20000.0/float64(fadeSamples)+1 {
		t.Fatalf("淡出跨回调必须连续, 边界前值: %d, 边界后值: %d", prev, border)
	}
	if !bytes.Equal(sil[(fadeSamples-200)*BytesPerSample:], make([]byte, 960-(fadeSamples-200)*BytesPerSample)) {
		t.Fatalf("淡出走完 %d 采样后必须全零", fadeSamples)
	}
}

func TestJitterBufferFadeOutNegativeSignal(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(bytes.Repeat([]byte{0x20, 0xD1}, targetSamples()))
	j.Read(make([]byte, 960))
	realLeft := targetSamples() - 480
	out := make([]byte, (realLeft+100)*BytesPerSample)
	j.Read(out)
	gap := out[realLeft*BytesPerSample:]
	if got := int16(binary.LittleEndian.Uint16(gap)); math.Abs(float64(got)-(-11950)) > 1 {
		t.Fatalf("负信号淡出首采样: %d, want -11950±1", got)
	}
	if got := int16(binary.LittleEndian.Uint16(gap[99*BytesPerSample:])); math.Abs(float64(got)-(-7000)) > 1 {
		t.Fatalf("负信号淡出第 100 采样: %d, want -7000±1", got)
	}
	sil := make([]byte, 960)
	j.Read(sil)
	if got := int16(binary.LittleEndian.Uint16(sil)); math.Abs(float64(got)-(-6950)) > 1 {
		t.Fatalf("负信号淡出跨回调续接: %d, want -6950±1", got)
	}
	if !bytes.Equal(sil[140*BytesPerSample:], make([]byte, 960-140*BytesPerSample)) {
		t.Fatalf("负信号淡出走完后必须全零")
	}
}

func TestJitterBufferSequentialContinuity(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(pcmSeq(0, targetSamples()*2))
	var got []byte
	for i := 0; i < 2; i++ {
		out := make([]byte, targetBufferByte)
		if j.Read(out) {
			t.Fatalf("第 %d 次读不应缺数据", i)
		}
		got = append(got, out...)
	}
	want := pcmSeq(0, targetSamples()*2)
	if !bytes.Equal(got[fadeSamples*BytesPerSample:], want[fadeSamples*BytesPerSample:]) {
		t.Fatalf("淡入区之外多次读取必须保持采样连续")
	}
}

func TestJitterBufferReset(t *testing.T) {
	j := NewJitterBuffer()
	j.Write(pcmSeq(0, targetSamples()))
	j.Reset()
	if !j.Read(make([]byte, 960)) {
		t.Fatalf("Reset 后应回到未攒满状态")
	}
	bufferedMs, _, _, _ := j.Stats()
	if bufferedMs != 0 {
		t.Fatalf("Reset 后 bufferedMs: %d, want 0", bufferedMs)
	}
}

func driftClock(j *JitterBuffer) *time.Duration {
	base := time.Now()
	offset := new(time.Duration)
	j.nowFn = func() time.Time { return base.Add(*offset) }
	return offset
}

func driftSample(j *JitterBuffer, idx int) int16 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return int16(binary.LittleEndian.Uint16(j.buf[idx*BytesPerSample:]))
}

func TestJitterBufferDriftBandBoundary(t *testing.T) {
	cases := []struct {
		name      string
		samples   int
		wantTrims uint64
	}{
		{"150ms整不触发", 7200, 0},
		{"150ms多1采样触发", 7201, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := NewJitterBuffer()
			driftClock(j)
			j.Write(make([]byte, c.samples*BytesPerSample))
			trims, _, _ := j.DriftStats()
			if trims != c.wantTrims {
				t.Fatalf("trims: %d, want %d", trims, c.wantTrims)
			}
		})
	}
}

func TestJitterBufferDriftTrimOnSilence(t *testing.T) {
	j := NewJitterBuffer()
	driftClock(j)
	j.Write(make([]byte, 7600*BytesPerSample))
	trims, inserts, forced := j.DriftStats()
	if trims != 1 || inserts != 0 || forced != 0 {
		t.Fatalf("trims/inserts/forced: %d/%d/%d, want 1/0/0", trims, inserts, forced)
	}
	bufferedMs, _, _, _ := j.Stats()
	if bufferedMs != (7600-driftPeriodSample)*1000/SampleRate {
		t.Fatalf("静音段丢一个周期后 bufferedMs: %d", bufferedMs)
	}
	out := make([]byte, 960)
	j.Read(out)
	if !bytes.Equal(out, make([]byte, 960)) {
		t.Fatalf("静音拼接后输出仍须全零")
	}
}

func TestJitterBufferDriftInsertBandBoundary(t *testing.T) {
	cases := []struct {
		name        string
		samples     int
		wantInserts uint64
	}{
		{"60ms整不触发", 2880, 0},
		{"60ms少1采样触发", 2879, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := NewJitterBuffer()
			driftClock(j)
			j.mu.Lock()
			j.primed = true
			j.mu.Unlock()
			j.Write(make([]byte, c.samples*BytesPerSample))
			_, inserts, _ := j.DriftStats()
			if inserts != c.wantInserts {
				t.Fatalf("inserts: %d, want %d", inserts, c.wantInserts)
			}
		})
	}
}

func TestJitterBufferDriftInsertUnprimed(t *testing.T) {
	j := NewJitterBuffer()
	driftClock(j)
	j.Write(make([]byte, 2500*BytesPerSample))
	_, inserts, _ := j.DriftStats()
	if inserts != 0 {
		t.Fatalf("未 primed 不应插入, inserts: %d", inserts)
	}
}

func TestJitterBufferDriftTrimCrossfadeForced(t *testing.T) {
	j := NewJitterBuffer()
	clock := driftClock(j)
	j.Write(pcmSeq(0, 8000))
	if trims, _, _ := j.DriftStats(); trims != 0 {
		t.Fatalf("非静音且未满强制等待不应丢, trims: %d", trims)
	}
	*clock += 61 * time.Second
	j.Write(pcmSeq(8000, 960))
	trims, _, forced := j.DriftStats()
	if trims != 1 || forced != 1 {
		t.Fatalf("trims/forced: %d/%d, want 1/1", trims, forced)
	}
	if got := driftSample(j, 0); got != 2 {
		t.Fatalf("交叉淡化首采样: %d, want round(480/241)=2", got)
	}
	if got := driftSample(j, driftFadeSample); got != 720 {
		t.Fatalf("淡化区外须接原序号 720, got %d", got)
	}
	bufferedMs, _, _, _ := j.Stats()
	if bufferedMs != (8960-driftPeriodSample)*1000/SampleRate {
		t.Fatalf("强制丢一个周期后 bufferedMs: %d", bufferedMs)
	}
}

func TestJitterBufferDriftInsertCrossfadeForced(t *testing.T) {
	j := NewJitterBuffer()
	clock := driftClock(j)
	j.mu.Lock()
	j.primed = true
	j.mu.Unlock()
	j.Write(pcmSeq(0, 2700))
	if _, inserts, _ := j.DriftStats(); inserts != 0 {
		t.Fatalf("非静音且未满强制等待不应插, inserts: %d", inserts)
	}
	*clock += 61 * time.Second
	j.Write(pcmSeq(2700, 100))
	_, inserts, forced := j.DriftStats()
	if inserts != 1 || forced != 1 {
		t.Fatalf("inserts/forced: %d/%d, want 1/1", inserts, forced)
	}
	if got := driftSample(j, driftFadeSample); got != 480 {
		t.Fatalf("重复段起点须为原序号 480, got %d", got)
	}
	if got := driftSample(j, driftFadeSample+driftPeriodSample); got != 957 {
		t.Fatalf("尾淡化首采样: %d, want round(960*240/241+240/241)=957", got)
	}
	if got := driftSample(j, 2*driftFadeSample+driftPeriodSample); got != 480 {
		t.Fatalf("尾淡化后须接原序号 480, got %d", got)
	}
	j.mu.Lock()
	n := len(j.buf) / BytesPerSample
	j.mu.Unlock()
	if n != 2800+driftPeriodSample {
		t.Fatalf("插入后采样数: %d, want %d", n, 2800+driftPeriodSample)
	}
}

func TestJitterBufferDriftMinGap(t *testing.T) {
	j := NewJitterBuffer()
	clock := driftClock(j)
	j.Write(make([]byte, 7600*BytesPerSample))
	if trims, _, _ := j.DriftStats(); trims != 1 {
		t.Fatalf("首次静音丢应发生, trims: %d", trims)
	}
	*clock += 9 * time.Second
	j.Write(make([]byte, 960*BytesPerSample))
	if trims, _, _ := j.DriftStats(); trims != 1 {
		t.Fatalf("间隔不足 10s 不应再丢, trims: %d", trims)
	}
	*clock += 2 * time.Second
	j.Write(make([]byte, 960*BytesPerSample))
	if trims, _, _ := j.DriftStats(); trims != 2 {
		t.Fatalf("间隔满 10s 应再丢, trims: %d", trims)
	}
}

func TestJitterBufferDriftNeedDirectionReset(t *testing.T) {
	j := NewJitterBuffer()
	clock := driftClock(j)
	j.mu.Lock()
	j.primed = true
	j.mu.Unlock()
	j.Write(bytes.Repeat([]byte{0x20, 0x4E}, 2500))
	*clock += 30 * time.Second
	j.Write(bytes.Repeat([]byte{0x20, 0x4E}, 6000))
	*clock += 31 * time.Second
	j.Write(bytes.Repeat([]byte{0x20, 0x4E}, 1500))
	if trims, inserts, forced := j.DriftStats(); trims != 0 || inserts != 0 || forced != 0 {
		t.Fatalf("方向切换后强制等待须重新计时, trims/inserts/forced: %d/%d/%d", trims, inserts, forced)
	}
	*clock += 30 * time.Second
	j.Write(bytes.Repeat([]byte{0x20, 0x4E}, 960))
	if trims, _, forced := j.DriftStats(); trims != 1 || forced != 1 {
		t.Fatalf("重新计时满 60s 应强制丢, trims/forced: %d/%d", trims, forced)
	}
}

func TestJitterBufferDriftResetClearsPending(t *testing.T) {
	j := NewJitterBuffer()
	clock := driftClock(j)
	j.Write(pcmSeq(0, 8000))
	j.Reset()
	*clock += 61 * time.Second
	j.Write(pcmSeq(0, 8000))
	if trims, _, forced := j.DriftStats(); trims != 0 || forced != 0 {
		t.Fatalf("Reset 后强制等待须从零计时, trims/forced: %d/%d", trims, forced)
	}
}
