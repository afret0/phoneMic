//go:build windows

package main

import (
	"log/slog"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func attachParentConsole() {
	attach := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	const attachParentProcess = ^uint32(0)
	if r, _, e := attach.Call(uintptr(attachParentProcess)); r == 0 {
		slog.Info("skip, 父进程无控制台, CLI 输出不可见, 日志仍写文件", "err", e)
		return
	}
	conout, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		slog.Error("fail, 打开 CONOUT$ 失败", "err", err)
		return
	}
	os.Stdout = conout
	os.Stderr = conout
	slog.Info("hit, 已挂回父终端控制台")
	waitForParentShell()
}

func waitForParentShell() {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	self := uint32(os.Getpid())
	var parent uint32
	var parentName string
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == self {
			parent = e.ParentProcessID
			parentName = windows.UTF16ToString(e.ExeFile[:])
			break
		}
	}
	if parent == 0 {
		return
	}
	switch strings.ToLower(parentName) {
	case "cmd.exe", "powershell.exe", "pwsh.exe", "windowsterminal.exe", "bash.exe", "conhost.exe":
	default:
		return
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, parent)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	windows.WaitForSingleObject(h, 60000)
}
