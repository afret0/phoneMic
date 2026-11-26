//go:build windows

package ui

import (
	_ "embed"
	"encoding/binary"
	"log/slog"
	"unsafe"
)

//go:embed assets/app.ico
var appIcon []byte

var (
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
)

const (
	wmSetIcon  = 0x0080
	iconSmall  = 0
	iconBig    = 1
	smCxIcon   = 11
	smCyIcon   = 12
	smCxSmIcon = 49
	smCySmIcon = 50
	icoVersion = 0x00030000
)

type iconEntry struct {
	width  int
	height int
	data   []byte
}

func parseIconEntries(b []byte) []iconEntry {
	if len(b) < 6 {
		return nil
	}
	n := int(binary.LittleEndian.Uint16(b[4:6]))
	entries := make([]iconEntry, 0, n)
	for i := 0; i < n; i++ {
		off := 6 + 16*i
		if off+16 > len(b) {
			break
		}
		e := b[off : off+16]
		w, h := int(e[0]), int(e[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		start := int(binary.LittleEndian.Uint32(e[12:16]))
		if start+size > len(b) {
			continue
		}
		entries = append(entries, iconEntry{width: w, height: h, data: b[start : start+size]})
	}
	return entries
}

func iconEntryScore(e iconEntry, cx, cy int) int {
	if e.width == cx && e.height == cy {
		return 0
	}
	if e.width >= cx && e.height >= cy {
		return 1 + e.width - cx + e.height - cy
	}
	return 1000 + cx - e.width + cy - e.height
}

func pickIconEntry(entries []iconEntry, cx, cy int) []byte {
	best := entries[0]
	bestScore := iconEntryScore(best, cx, cy)
	for _, e := range entries[1:] {
		if s := iconEntryScore(e, cx, cy); s < bestScore {
			best, bestScore = e, s
		}
	}
	return best.data
}

func systemMetric(i int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(i))
	return int(r)
}

func (a *App) applyIcon(hwnd, kind uintptr, entries []iconEntry, cx, cy int) {
	data := pickIconEntry(entries, cx, cy)
	h, _, err := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, icoVersion,
		uintptr(cx), uintptr(cy), 0,
	)
	if h == 0 {
		slog.Error("fail, 创建窗口图标失败", "kind", kind, "cx", cx, "cy", cy, "err", err)
		return
	}
	procSendMessageW.Call(hwnd, wmSetIcon, kind, h)
}

func (a *App) setWindowIcon() {
	hwnd := a.hook.hwnd
	if hwnd == 0 {
		slog.Info("skip, 窗口句柄为空, 不设置窗口图标")
		return
	}
	entries := parseIconEntries(appIcon)
	if len(entries) == 0 {
		slog.Error("fail, 内嵌 app.ico 解析为空")
		return
	}
	a.applyIcon(hwnd, iconSmall, entries, systemMetric(smCxSmIcon), systemMetric(smCySmIcon))
	a.applyIcon(hwnd, iconBig, entries, systemMetric(smCxIcon), systemMetric(smCyIcon))
	slog.Info("done, 窗口与任务栏图标已设置")
}