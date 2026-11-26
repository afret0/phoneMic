package ui

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/afret0/phoneMic/internal/audio"
	"github.com/afret0/phoneMic/internal/certs"
	"github.com/afret0/phoneMic/internal/driver"
	"github.com/afret0/phoneMic/internal/server"
	"github.com/afret0/phoneMic/internal/update"
	"github.com/afret0/phoneMic/internal/version"
	qrcode "github.com/skip2/go-qrcode"
	webview "github.com/webview/webview_go"
)

type DeviceView struct {
	Current string `json:"current"`
	Match   string `json:"match"`
}

type StateView struct {
	Driver    driver.State   `json:"driver"`
	Status    map[string]any `json:"status"`
	Device    DeviceView     `json:"device"`
	Endpoints []string       `json:"endpoints"`
	CertURL   string         `json:"certUrl"`
	MicName   string         `json:"micName"`
	Version   string         `json:"version"`
	OS        string         `json:"os"`
	Update    update.State   `json:"update"`
}

const playerStallTimeout = 3 * time.Second

type App struct {
	srv       *server.Server
	mgr       *driver.Manager
	upd       *update.Manager
	httpsAddr string
	httpAddr  string
	quitFn    func()

	matchMu   sync.Mutex
	match     string
	connectMu sync.Mutex
	done      atomic.Bool

	cbName  string
	cbTotal uint64
	cbSince time.Time

	wv   webview.WebView
	wvMu sync.Mutex
	hook windowHook
}

func NewApp(srv *server.Server, mgr *driver.Manager, upd *update.Manager, match, httpsAddr, httpAddr string, quit func()) *App {
	return &App{srv: srv, mgr: mgr, upd: upd, match: match, httpsAddr: httpsAddr, httpAddr: httpAddr, quitFn: quit}
}

func (a *App) Run() {
	go a.runTray()
	go a.watch()
	a.runWindow()
	a.stopTray()
}

func (a *App) Quit() {
	if a.done.Swap(true) {
		return
	}
	slog.Info("recv, 退出程序")
	a.quitFn()
	a.terminateWindow()
}

func (a *App) currentMatch() string {
	a.matchMu.Lock()
	defer a.matchMu.Unlock()
	return a.match
}

func (a *App) bindings() map[string]any {
	return map[string]any{
		"GetState":        a.getState,
		"ListDevices":     a.ListDevices,
		"InstallDriver":   a.installDriver,
		"UninstallDriver": a.uninstallDriver,
		"UpdateDriver":    a.updateDriver,
		"SwitchDevice":    a.SwitchDevice,
		"QR":              a.qr,
		"QuitApp":         func() { a.Quit() },
		"CheckUpdate":     a.upd.TriggerCheck,
		"ApplyUpdate":     a.upd.TriggerDownload,
		"OpenUpdatePage":  update.OpenReleasePage,
		"RestartApp":      a.restartApp,
	}
}

func (a *App) restartApp() error {
	if err := a.upd.Restart(); err != nil {
		return err
	}
	a.Quit()
	return nil
}

func (a *App) getState() (StateView, error) {
	_, httpPort, _ := net.SplitHostPort(a.httpAddr)
	var certURL string
	if ips := certs.ReachableIPv4s(); len(ips) > 0 {
		certURL = fmt.Sprintf("http://%s:%s/ca.crt", ips[0].String(), httpPort)
	}
	mic := "BlackHole 2ch"
	if runtime.GOOS == "windows" {
		mic = "CABLE Output (VB-Audio Virtual Cable)"
	}
	return StateView{
		Driver:    a.mgr.State(),
		Status:    a.srv.Status(),
		Device:    DeviceView{Current: a.srv.PlayerName(), Match: a.currentMatch()},
		Endpoints: server.BannerURLs(a.httpsAddr),
		CertURL:   certURL,
		MicName:   mic,
		Version:   version.Version,
		OS:        runtime.GOOS,
		Update:    a.upd.State(),
	}, nil
}

func (a *App) ListDevices() ([]audio.OutputDevice, error) {
	devices, err := audio.ListOutputDevices()
	if err != nil {
		slog.Error("fail, 枚举输出设备失败", "err", err)
		return nil, err
	}
	return devices, nil
}

func (a *App) installDriver() bool   { return a.mgr.TriggerInstall() }
func (a *App) uninstallDriver() bool { return a.mgr.TriggerUninstall() }
func (a *App) updateDriver() bool    { return a.mgr.TriggerUpdate() }

func (a *App) DeviceAuto() bool {
	return a.currentMatch() == driver.DefaultMatch
}

func (a *App) SwitchDevice(name string) error {
	a.connectMu.Lock()
	defer a.connectMu.Unlock()
	match := name
	if match == "" {
		match = driver.DefaultMatch
	}
	p, err := audio.NewPlayer(match)
	if err != nil {
		slog.Error("fail, 切换输出设备失败", "match", match, "err", err)
		return err
	}
	a.matchMu.Lock()
	a.match = match
	a.matchMu.Unlock()
	a.srv.SetPlayer(p)
	return nil
}

func (a *App) qr(text string) (string, error) {
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func (a *App) watch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for !a.done.Load() {
		<-ticker.C
		a.ensureDefaultPlayer()
		a.checkPlayerAlive()
	}
}

func (a *App) ensureDefaultPlayer() {
	a.connectMu.Lock()
	defer a.connectMu.Unlock()
	if a.currentMatch() != driver.DefaultMatch {
		return
	}
	st := a.mgr.State()
	has := a.srv.PlayerName() != ""
	if st.Loaded && !has {
		p, err := audio.NewPlayer(driver.DefaultMatch)
		if err != nil {
			slog.Error("fail, 虚拟声卡已加载但打开失败", "err", err)
			return
		}
		a.srv.SetPlayer(p)
	}
	if !st.Installed && has {
		a.srv.SetPlayer(nil)
	}
}

func (a *App) checkPlayerAlive() {
	a.connectMu.Lock()
	defer a.connectMu.Unlock()
	name := a.srv.PlayerName()
	if name == "" {
		a.cbName = ""
		return
	}
	total, _ := a.srv.Status()["totalCallbacks"].(uint64)
	now := time.Now()
	if name != a.cbName || total != a.cbTotal {
		a.cbName, a.cbTotal, a.cbSince = name, total, now
		return
	}
	if now.Sub(a.cbSince) < playerStallTimeout {
		return
	}
	slog.Error("fail, 输出设备回调停止疑似失效, 重建播放器", "device", name, "totalCallbacks", total, "stalledSec", int(now.Sub(a.cbSince).Seconds()))
	match := a.currentMatch()
	p, err := audio.NewPlayer(match)
	if err != nil {
		slog.Error("fail, 失效播放器重建失败", "match", match, "err", err)
		a.cbSince = now
		return
	}
	a.srv.SetPlayer(p)
	a.cbName, a.cbTotal, a.cbSince = p.DeviceName(), 0, now
}
