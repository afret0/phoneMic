package server

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeSenderName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"正常型号原样保留", "Xiaomi 23127PN0CC", "Xiaomi 23127PN0CC"},
		{"浏览器简名保留", "Chrome · Android", "Chrome · Android"},
		{"空串", "", ""},
		{"首尾空白去除", "  Pixel 8  ", "Pixel 8"},
		{"控制字符剔除", "Pi\x00xel\n8\r\t", "Pixel8"},
		{"恰好64字符不截断", strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"超过64字符截断", strings.Repeat("a", 65), strings.Repeat("a", 64)},
		{"多字节按字符数截断", strings.Repeat("汉", 70), strings.Repeat("汉", 64)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeSenderName(c.raw); got != c.want {
				t.Fatalf("sanitizeSenderName(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestComputerName(t *testing.T) {
	hn, err := os.Hostname()
	if err != nil || hn == "" {
		t.Skip("本机取不到主机名")
	}
	want := strings.TrimSuffix(hn, ".local")
	if got := computerName(); got != want {
		t.Fatalf("computerName() = %q, want %q", got, want)
	}
}

func TestStatusIdentityFields(t *testing.T) {
	s := &Server{}
	st := s.Status()
	if st["computer"] != computerName() {
		t.Fatalf("空闲态 computer = %+v, want %q", st["computer"], computerName())
	}
	if _, ok := st["sender"]; ok {
		t.Fatal("空闲态不应有 sender")
	}

	s.current = &session{remote: "192.168.1.23:54321", name: "Xiaomi 23127PN0CC"}
	st = s.Status()
	if st["sender"] != "Xiaomi 23127PN0CC" {
		t.Fatalf("推流态 sender = %+v", st["sender"])
	}
	if st["remote"] != "192.168.1.23:54321" {
		t.Fatalf("推流态 remote = %+v", st["remote"])
	}
	if !utf8.ValidString(st["computer"].(string)) {
		t.Fatal("computer 非法 utf8")
	}
}
