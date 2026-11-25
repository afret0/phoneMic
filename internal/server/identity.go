package server

import (
	"os"
	"strings"
	"unicode/utf8"
)

const maxSenderNameRunes = 64

func computerName() string {
	hn, err := os.Hostname()
	if err != nil || hn == "" {
		return ""
	}
	return strings.TrimSuffix(hn, ".local")
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
