package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
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
			if !c.wantSilence && binary.LittleEndian.Uint16(out) != 1 {
				t.Fatalf("达标后应输出真实数据, 首采样: %d", binary.LittleEndian.Uint16(out))
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
	j.Write(make([]byte, maxBufferByte))
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
	if binary.LittleEndian.Uint16(out) != 7 {
		t.Fatalf("已有数据应先输出, 首采样: %d", binary.LittleEndian.Uint16(out))
	}
	if !bytes.Equal(out[targetBufferByte:], make([]byte, targetBufferByte)) {
		t.Fatalf("缺口必须补零")
	}
	_, _, underrun, _ := j.Stats()
	if underrun != uint64(targetBufferByte) {
		t.Fatalf("underrun: %d, want %d", underrun, targetBufferByte)
	}
	j.Write(pcmSeq(9, 100))
	if !j.Read(make([]byte, 960)) {
		t.Fatalf("underrun 后必须重新攒够目标量才恢复输出")
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
	if !bytes.Equal(got, want) {
		t.Fatalf("多次读取必须保持采样连续")
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
