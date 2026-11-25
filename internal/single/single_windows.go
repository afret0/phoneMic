//go:build windows

package single

import (
	"log/slog"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const quitEventName = `Local\phoneMic.quit`

type oldProcess struct {
	pid  uint32
	name string
}

func TakeOver() {
	old := findOld()
	if len(old) == 0 {
		return
	}
	graceful := notifyQuit()
	slog.Info("recv, 检测到旧实例, 开始单例接管", "count", len(old), "graceful", graceful)
	deadline := time.Now().Add(gracePeriod)
	for _, p := range old {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, p.pid)
		if err != nil {
			slog.Info("hit, 旧实例已退出", "pid", p.pid, "name", p.name)
			continue
		}
		if graceful && waitExit(h, deadline) {
			slog.Info("done, 旧实例已优雅退出", "pid", p.pid, "name", p.name)
			windows.CloseHandle(h)
			continue
		}
		forceKill(h, p)
		windows.CloseHandle(h)
	}
}

func WatchQuit(onQuit func()) {
	name, err := windows.UTF16PtrFromString(quitEventName)
	if err != nil {
		slog.Error("fail, 退出通知事件名转换失败", "err", err)
		return
	}
	h, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil {
		slog.Error("fail, 创建退出通知事件失败, 后续新实例只能强杀本实例", "err", err)
		return
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, windows.INFINITE)
	if err != nil {
		slog.Error("fail, 等待退出通知失败", "err", err)
		return
	}
	if ev == windows.WAIT_OBJECT_0 {
		slog.Info("recv, 收到新实例接管通知, 优雅退出")
		onQuit()
	}
}

func findOld() []oldProcess {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		slog.Error("fail, 枚举进程失败, 跳过单例接管", "err", err)
		return nil
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	self := uint32(os.Getpid())
	var out []oldProcess
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == self {
			continue
		}
		name := windows.UTF16ToString(e.ExeFile[:])
		if isPhoneMicProcess(name) {
			out = append(out, oldProcess{pid: e.ProcessID, name: name})
		}
	}
	return out
}

func notifyQuit() bool {
	name, err := windows.UTF16PtrFromString(quitEventName)
	if err != nil {
		slog.Error("fail, 退出通知事件名转换失败", "err", err)
		return false
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		slog.Info("hit, 旧实例无优雅退出通道(旧版本), 直接强杀", "err", err)
		return false
	}
	defer windows.CloseHandle(h)
	if err := windows.SetEvent(h); err != nil {
		slog.Error("fail, 发送退出通知失败, 转为强杀", "err", err)
		return false
	}
	return true
}

func waitExit(h windows.Handle, deadline time.Time) bool {
	remain := time.Until(deadline)
	if remain <= 0 {
		return false
	}
	ev, err := windows.WaitForSingleObject(h, uint32(remain.Milliseconds()))
	return err == nil && ev == windows.WAIT_OBJECT_0
}

func forceKill(h windows.Handle, p oldProcess) {
	if err := windows.TerminateProcess(h, 1); err != nil {
		slog.Error("fail, 强杀旧实例失败(可能权限不足)", "pid", p.pid, "name", p.name, "err", err)
		return
	}
	if waitExit(h, time.Now().Add(killConfirm)) {
		slog.Info("done, 旧实例已强杀", "pid", p.pid, "name", p.name)
	} else {
		slog.Error("fail, 强杀后旧实例仍未退出", "pid", p.pid, "name", p.name)
	}
}
