//go:build windows

package ui

import (
	"log/slog"
	"syscall"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procIsIconic         = user32.NewProc("IsIconic")
)

const (
	gwlpWndproc = ^uintptr(3)
	wmClose     = 0x0010
	swHide      = 0
	swShow      = 5
	swRestore   = 9
)

type windowHook struct {
	hwnd     uintptr
	origProc uintptr
	cb       uintptr
}

func (a *App) hookClose() {
	hwnd := uintptr(a.wv.Window())
	a.hook.hwnd = hwnd
	a.hook.cb = syscall.NewCallback(func(h uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == wmClose {
			procShowWindow.Call(h, swHide)
			slog.Info("hit, 窗口关闭被拦截, 已收进托盘继续推流")
			return 0
		}
		ret, _, _ := procCallWindowProc.Call(a.hook.origProc, h, uintptr(msg), wp, lp)
		return ret
	})
	a.hook.origProc, _, _ = procGetWindowLongPtr.Call(hwnd, gwlpWndproc)
	procSetWindowLongPtr.Call(hwnd, gwlpWndproc, a.hook.cb)
}

func (a *App) Show() {
	if a.hook.hwnd == 0 {
		return
	}
	if r, _, _ := procIsIconic.Call(a.hook.hwnd); r != 0 {
		procShowWindow.Call(a.hook.hwnd, swRestore)
	} else {
		procShowWindow.Call(a.hook.hwnd, swShow)
	}
	procSetForeground.Call(a.hook.hwnd)
}