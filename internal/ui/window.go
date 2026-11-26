package ui

import (
	_ "embed"
	"log/slog"

	webview "github.com/webview/webview_go"
)

//go:embed desktop.html
var desktopHTML string

func (a *App) runWindow() {
	if a.done.Load() {
		return
	}
	w := webview.New(false)
	defer w.Destroy()
	a.wvMu.Lock()
	a.wv = w
	a.wvMu.Unlock()
	if a.done.Load() {
		return
	}
	w.SetTitle("phoneMic · 手机麦克风")
	w.SetSize(520, 880, webview.HintNone)
	for name, fn := range a.bindings() {
		if err := w.Bind(name, fn); err != nil {
			slog.Error("fail, 绑定 UI 桥接失败", "name", name, "err", err)
		}
	}
	w.SetHtml(desktopHTML)
	a.hookClose()
	a.setWindowIcon()
	slog.Info("done, 桌面窗口已打开")
	w.Run()
	slog.Info("done, 桌面窗口消息循环退出")
}

func (a *App) terminateWindow() {
	a.wvMu.Lock()
	w := a.wv
	a.wvMu.Unlock()
	if w != nil {
		w.Dispatch(w.Terminate)
	}
}