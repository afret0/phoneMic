//go:build windows

package driver

import (
	"encoding/binary"
	"testing"
)

func TestParseDeviceFormatRate(t *testing.T) {
	blob := func(rate uint32, size int) []byte {
		b := make([]byte, size)
		if size >= 26 {
			binary.LittleEndian.PutUint16(b[8:10], 0xFFFE)
			binary.LittleEndian.PutUint32(b[12:16], rate)
		}
		return b
	}
	realCableInput48k := []byte{
		0x41, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00,
		0xFE, 0xFF, 0x02, 0x00, 0x80, 0xBB, 0x00, 0x00,
		0x00, 0x65, 0x04, 0x00, 0x06, 0x00, 0x18, 0x00,
		0x16, 0x00, 0x18, 0x00, 0x03, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
		0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71,
	}
	cases := []struct {
		name string
		blob []byte
		want int
	}{
		{"实测CABLEInput48k完整blob", realCableInput48k, 48000},
		{"标准48k波形格式", blob(48000, 40), 48000},
		{"44k1需要提示改格式", blob(44100, 26), 44100},
		{"刚好26字节可解出采样率", blob(96000, 26), 96000},
		{"非扩展波形格式返回未知", blob(48000, 18), 0},
		{"空数据返回未知", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDeviceFormatRate(c.blob); got != c.want {
				t.Fatalf("rate: %d, want %d", got, c.want)
			}
		})
	}
}
