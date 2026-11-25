//go:build !windows

package ui

import "log/slog"

func (a *App) runTray() {
	slog.Info("skip, 托盘暂未在此平台启用, 关闭窗口即退出")
}

func (a *App) stopTray() {}