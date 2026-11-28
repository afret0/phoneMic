package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/afret0/phoneMic/internal/audio"
	"github.com/afret0/phoneMic/internal/certs"
	"github.com/afret0/phoneMic/internal/driver"
	"github.com/afret0/phoneMic/internal/server"
	"github.com/afret0/phoneMic/internal/single"
	"github.com/afret0/phoneMic/internal/ui"
	"github.com/afret0/phoneMic/internal/update"
	"github.com/afret0/phoneMic/internal/usb"
)

func main() {
	device := flag.String("device", driver.DefaultMatch, "声音写入设备名匹配子串(不区分大小写)")
	listDevices := flag.Bool("list-devices", false, "列出所有声音写入设备后退出")
	installDriver := flag.Bool("install-driver", false, "安装虚拟声卡(mac: BlackHole / win: VB-Cable), 需管理员授权")
	uninstallDriver := flag.Bool("uninstall-driver", false, "卸载虚拟声卡, 需管理员授权")
	httpsAddr := flag.String("https-addr", ":8443", "HTTPS 监听地址(手机页面/推流)")
	httpAddr := flag.String("http-addr", ":8080", "HTTP 监听地址(仅CA证书下载与跳转)")
	certsDir := flag.String("certs-dir", defaultCertsDir(), "自签CA与服务证书存放目录(默认在程序所在目录, 保证手机端已信任的 CA 持久复用)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(openLogSink(), os.Stdout), nil)))
	if *listDevices || *installDriver || *uninstallDriver {
		attachParentConsole()
		slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(openLogSink(), os.Stdout), nil)))
	}

	switch {
	case *listDevices:
		devices, err := audio.ListOutputDevices()
		if err != nil {
			slog.Error("fail, 枚举输出设备失败", "err", err)
			os.Exit(1)
		}
		for _, d := range devices {
			marker := ""
			if d.IsDefault {
				marker = "  (系统默认)"
			}
			fmt.Printf("%s%s\n", d.Name, marker)
		}
		return
	case *installDriver:
		if err := driver.Install(); err != nil {
			slog.Error("fail, 虚拟声卡安装失败", "err", err)
			os.Exit(1)
		}
		return
	case *uninstallDriver:
		if err := driver.Uninstall(); err != nil {
			slog.Error("fail, 虚拟声卡卸载失败", "err", err)
			os.Exit(1)
		}
		return
	}

	single.TakeOver()

	mgr := driver.NewManager()
	if *device == driver.DefaultMatch && !driver.Installed() {
		slog.Info("recv, 未检测到虚拟声卡, 后台自动安装(需系统管理员授权)")
		mgr.TriggerInstall()
	}

	bundle, err := certs.Ensure(*certsDir, certs.LocalIPv4s())
	if err != nil {
		slog.Error("fail, 证书初始化失败", "err", err)
		os.Exit(1)
	}
	dataDir, err := os.UserConfigDir()
	if err != nil {
		dataDir = filepath.Dir(*certsDir)
		slog.Error("fail, 取用户配置目录失败, 数据目录改用证书目录上级", "dataDir", dataDir, "err", err)
	}
	server.InitComputerName(filepath.Join(dataDir, "phoneMic"))

	var usbPorts []int
	for _, addr := range []string{*httpsAddr, *httpAddr} {
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.Atoi(p); err == nil {
				usbPorts = append(usbPorts, n)
			}
		}
	}
	usbMgr := usb.NewManager(filepath.Join(dataDir, "phoneMic", "usb"), usbPorts...)

	type playerResult struct {
		p   *audio.Player
		err error
	}
	playerCh := make(chan playerResult, 1)
	go func() {
		runtime.LockOSThread()
		p, perr := audio.NewPlayer(*device)
		playerCh <- playerResult{p, perr}
	}()
	pr := <-playerCh
	var player *audio.Player
	if pr.err != nil {
		slog.Info("hit, 输出设备暂不可用, 等待驱动安装完成或在面板中选择", "match", *device, "err", pr.err)
	} else {
		player = pr.p
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(player, bundle, *httpsAddr, *httpAddr)
	srvDone := make(chan struct{})
	srvErr := make(chan error, 1)
	go func() {
		err := srv.Run(ctx)
		close(srvDone)
		if err != nil {
			slog.Error("fail, 服务退出", "err", err)
			srvErr <- err
		}
	}()

	upd := update.NewManager()
	upd.TriggerCheck()
	go usbMgr.Run(ctx)

	app := ui.NewApp(srv, mgr, upd, usbMgr, *device, *httpsAddr, *httpAddr, stop)
	srv.ListDevices = app.ListDevices
	srv.SwitchDevice = app.SwitchDevice
	srv.SwitchMode = app.SwitchMode
	srv.DeviceAuto = app.DeviceAuto
	go single.WatchQuit(app.Quit)
	go func() {
		select {
		case <-srvErr:
			app.Quit()
		case <-ctx.Done():
			app.Quit()
		}
	}()

	deviceName := srv.PlayerName()
	if deviceName == "" {
		deviceName = "(等待驱动安装或在面板中选择)"
	}
	printBanner(*httpsAddr, *httpAddr, deviceName)

	app.Run()
	stop()
	<-srvDone
	srv.SetPlayer(nil)
	srv.SetCapturer(nil)
	slog.Info("done, 程序已退出")
}

func defaultCertsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "certs"
	}
	return filepath.Join(filepath.Dir(exe), "certs")
}

func openLogSink() io.Writer {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	logDir := filepath.Join(dir, "phoneMic")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return os.Stderr
	}
	f, err := os.OpenFile(filepath.Join(logDir, "phone_mic.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return os.Stderr
	}
	return f
}

func printBanner(httpsAddr, httpAddr, deviceName string) {
	micName := "BlackHole 2ch"
	if runtime.GOOS == "windows" {
		micName = "CABLE Output (VB-Audio Virtual Cable)"
	}
	fmt.Println("========================================================")
	fmt.Println("  phoneMic · 手机就是你的电脑麦克风")
	fmt.Println("--------------------------------------------------------")
	for _, u := range server.BannerURLs(httpsAddr) {
		fmt.Printf("  手机浏览器打开: %s\n", u)
	}
	_, httpPort, err := net.SplitHostPort(httpAddr)
	if err == nil {
		for _, ip := range certs.ReachableIPv4s() {
			fmt.Printf("  首次使用先装证书: http://%s:%s/ca.crt\n", ip.String(), httpPort)
			break
		}
	}
	fmt.Printf("  声音写入设备: %s\n", deviceName)
	fmt.Printf("  会议/聊天 App 的麦克风请选: %s\n", micName)
	fmt.Println("========================================================")
}
