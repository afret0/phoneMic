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
	a.wvMu.Lock()
	a.wv = w
	a.wvMu.Unlock()
	if a.done.Load() {
		w.Destroy()
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
	a.wvMu.Lock()
	a.wv = nil
	w.Destroy()
	a.wvMu.Unlock()
	slog.Info("done, 桌面窗口消息循环退出")
}

func (a *App) terminateWindow() {
	a.wvMu.Lock()
	defer a.wvMu.Unlock()
	if a.wv != nil {
		a.wv.Dispatch(a.wv.Terminate)
	}
}
