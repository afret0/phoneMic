package server

import (
	"crypto/rand"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const maxSenderNameRunes = 64

const randomNameAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

var computerNameState struct {
	mu   sync.Mutex
	name string
}

func InitComputerName(dir string) {
	hn, err := os.Hostname()
	if err != nil {
		hn = ""
		slog.Error("fail, 读取主机名失败, 改用随机电脑名", "err", err)
	}
	computerNameState.mu.Lock()
	defer computerNameState.mu.Unlock()
	computerNameState.name = ensureComputerName(dir, hn)
}

func ensureComputerName(dir, hostname string) string {
	if name := sanitizeSenderName(strings.TrimSuffix(hostname, ".local")); name != "" {
		slog.Info("done, 本机电脑名取自主机名", "name", name)
		return name
	}
	path := filepath.Join(dir, "computer_name")
	if b, err := os.ReadFile(path); err == nil {
		if saved := sanitizeSenderName(string(b)); saved != "" {
			slog.Info("hit, 沿用已保存的随机电脑名", "name", saved)
			return saved
		}
	}
	name := randomDeviceName("PC")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error("fail, 创建数据目录失败, 随机电脑名仅本次运行有效", "dir", dir, "name", name, "err", err)
		return name
	}
	if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
		slog.Error("fail, 持久化随机电脑名失败, 随机名仅本次运行有效", "path", path, "name", name, "err", err)
		return name
	}
	slog.Info("done, 主机名不可用, 已生成随机电脑名并持久化", "name", name, "path", path)
	return name
}

func computerName() string {
	computerNameState.mu.Lock()
	defer computerNameState.mu.Unlock()
	if computerNameState.name != "" {
		return computerNameState.name
	}
	hn, err := os.Hostname()
	if err != nil {
		return ""
	}
	return sanitizeSenderName(strings.TrimSuffix(hn, ".local"))
}

func randomDeviceName(prefix string) string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	out := make([]byte, 0, len(prefix)+5)
	out = append(out, prefix...)
	out = append(out, '-')
	for _, b := range buf {
		out = append(out, randomNameAlphabet[int(b)%len(randomNameAlphabet)])
	}
	return string(out)
}

func sanitizeSenderName(raw string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	clean = strings.TrimSpace(clean)
	if utf8.RuneCountInString(clean) <= maxSenderNameRunes {
		return clean
	}
	runes := []rune(clean)
	return string(runes[:maxSenderNameRunes])
}
