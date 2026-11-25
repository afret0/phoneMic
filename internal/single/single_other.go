//go:build !darwin && !windows

package single

import "log/slog"

func TakeOver() {
	slog.Info("skip, 当前平台未实现单例接管")
}

func WatchQuit(func()) {}
