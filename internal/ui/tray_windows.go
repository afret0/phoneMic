//go:build windows

package ui

import (
	_ "embed"

	"github.com/energye/systray"
)

//go:embed assets/tray.ico
var trayIcon []byte

func (a *App) runTray() {
	systray.Register(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("phoneMic · 手机就是你的电脑麦克风")
		open := systray.AddMenuItem("打开面板", "显示 phoneMic 窗口")
		open.Click(func() { a.Show() })
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出", "停止服务并退出 phoneMic")
		quit.Click(func() { a.Quit() })
	}, nil)
}

func (a *App) stopTray() { systray.Quit() }