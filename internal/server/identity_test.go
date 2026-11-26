package server

import (
	"os"
	"path/filepath"
	"regexp"
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
	want := sanitizeSenderName(strings.TrimSuffix(hn, ".local"))
	if got := computerName(); got != want {
		t.Fatalf("computerName() = %q, want %q", got, want)
	}
}

func TestComputerNameCached(t *testing.T) {
	computerNameState.mu.Lock()
	old := computerNameState.name
	computerNameState.name = "PC-TEST"
	computerNameState.mu.Unlock()
	defer func() {
		computerNameState.mu.Lock()
		computerNameState.name = old
		computerNameState.mu.Unlock()
	}()
	if got := computerName(); got != "PC-TEST" {
		t.Fatalf("缓存名未生效, got %q", got)
	}
}

func TestEnsureComputerName(t *testing.T) {
	randomPat := regexp.MustCompile(`^PC-[A-HJ-KM-NP-Z2-9]{4}$`)

	t.Run("主机名可用时优先且不落盘", func(t *testing.T) {
		dir := t.TempDir()
		if got := ensureComputerName(dir, "MyMac.local"); got != "MyMac" {
			t.Fatalf("got %q, want MyMac", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "computer_name")); !os.IsNotExist(err) {
			t.Fatal("主机名可用时不应写随机名文件")
		}
	})
	t.Run("主机名空白视为拿不到", func(t *testing.T) {
		got := ensureComputerName(t.TempDir(), " \x00\t")
		if !randomPat.MatchString(got) {
			t.Fatalf("got %q, want PC-XXXX", got)
		}
	})
	t.Run("主机名为空生成随机名并持久化复用", func(t *testing.T) {
		dir := t.TempDir()
		first := ensureComputerName(dir, "")
		if !randomPat.MatchString(first) {
			t.Fatalf("got %q, want PC-XXXX", first)
		}
		b, err := os.ReadFile(filepath.Join(dir, "computer_name"))
		if err != nil || string(b) != first {
			t.Fatalf("随机名未持久化, file %q, err %v", b, err)
		}
		if second := ensureComputerName(dir, ""); second != first {
			t.Fatalf("二次启动名字变了: %q -> %q", first, second)
		}
	})
	t.Run("已保存合法名直接沿用", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "computer_name"), []byte("PC-AB2C\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ensureComputerName(dir, ""); got != "PC-AB2C" {
			t.Fatalf("got %q, want PC-AB2C", got)
		}
	})
	t.Run("目录不可写仍返回随机名", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := ensureComputerName(filepath.Join(f, "sub"), "")
		if !randomPat.MatchString(got) {
			t.Fatalf("got %q, want PC-XXXX", got)
		}
	})
}

func TestRandomDeviceName(t *testing.T) {
	pat := regexp.MustCompile(`^Phone-[A-HJ-KM-NP-Z2-9]{4}$`)
	for i := 0; i < 20; i++ {
		if got := randomDeviceName("Phone"); !pat.MatchString(got) {
			t.Fatalf("got %q, want Phone-XXXX", got)
		}
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
