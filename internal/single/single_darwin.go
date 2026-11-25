//go:build darwin

package single

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type oldProcess struct {
	pid  int
	name string
}

func TakeOver() {
	old := findOld()
	if len(old) == 0 {
		return
	}
	slog.Info("recv, 检测到旧实例, 开始单例接管", "count", len(old), "graceful", true)
	deadline := time.Now().Add(gracePeriod)
	for _, p := range old {
		if err := syscall.Kill(p.pid, syscall.SIGTERM); err != nil {
			slog.Info("hit, 旧实例已退出", "pid", p.pid, "name", p.name)
			continue
		}
		if waitExit(p.pid, deadline) {
			slog.Info("done, 旧实例已优雅退出", "pid", p.pid, "name", p.name)
			continue
		}
		if err := syscall.Kill(p.pid, syscall.SIGKILL); err != nil {
			slog.Error("fail, 强杀旧实例失败(可能权限不足)", "pid", p.pid, "name", p.name, "err", err)
			continue
		}
		if waitExit(p.pid, time.Now().Add(killConfirm)) {
			slog.Info("done, 旧实例已强杀", "pid", p.pid, "name", p.name)
		} else {
			slog.Error("fail, 强杀后旧实例仍未退出", "pid", p.pid, "name", p.name)
		}
	}
}

func WatchQuit(func()) {}

func findOld() []oldProcess {
	out, err := exec.Command("ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		slog.Error("fail, 枚举进程失败, 跳过单例接管", "err", err)
		return nil
	}
	self := os.Getpid()
	var old []oldProcess
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidStr, comm, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid == self {
			continue
		}
		name := filepath.Base(strings.TrimSpace(comm))
		if isPhoneMicProcess(name) {
			old = append(old, oldProcess{pid: pid, name: name})
		}
	}
	return old
}

func waitExit(pid int, deadline time.Time) bool {
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}
