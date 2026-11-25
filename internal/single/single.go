package single

import (
	"strings"
	"time"
)

const (
	gracePeriod = 3 * time.Second
	killConfirm = 2 * time.Second
)

func isPhoneMicProcess(exeName string) bool {
	n := strings.TrimSuffix(strings.ToLower(exeName), ".exe")
	return strings.HasPrefix(n, "phone_mic") || strings.HasPrefix(n, "phonemic")
}
