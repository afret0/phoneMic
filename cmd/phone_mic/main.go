package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/afret0/phoneMic/internal/audio"
	"github.com/afret0/phoneMic/internal/certs"
	"github.com/afret0/phoneMic/internal/driver"
	"github.com/afret0/phoneMic/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	device := flag.String("device", driver.DefaultMatch, "输出设备名匹配子串(不区分大小写)")
	listDevices := flag.Bool("list-devices", false, "列出所有输出设备后退出")
	installDriver := flag.Bool("install-driver", false, "安装虚拟声卡(mac: BlackHole / win: VB-Cable), 需管理员授权")
	uninstallDriver := flag.Bool("uninstall-driver", false, "卸载虚拟声卡, 需管理员授权")
	httpsAddr := flag.String("https-addr", ":8443", "HTTPS 监听地址(手机页面/推流)")
	httpAddr := flag.String("http-addr", ":8080", "HTTP 监听地址(仅CA证书下载与跳转)")
	certsDir := flag.String("certs-dir", "certs", "自签CA与服务证书存放目录")
	flag.Parse()

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

	if *device == driver.DefaultMatch && !driver.Installed() {
		slog.Error("fail, 未检测到虚拟声卡", "hint", "先运行 ./phone_mic --install-driver 安装(需管理员授权), 或用 --device 指定其它输出设备")
		os.Exit(1)
	}

	bundle, err := certs.Ensure(*certsDir, certs.LocalIPv4s())
	if err != nil {
		slog.Error("fail, 证书初始化失败", "err", err)
		os.Exit(1)
	}

	player, err := audio.NewPlayer(*device)
	if err != nil {
		if driver.Installed() {
			slog.Error("fail, 打开输出设备失败, 驱动已安装但设备未加载, 请重启电脑后再试", "match", *device, "err", err)
		} else {
			slog.Error("fail, 打开输出设备失败", "match", *device, "err", err)
		}
		os.Exit(1)
	}
	defer player.Close()

	printBanner(*httpsAddr, *httpAddr, player.DeviceName())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(player, bundle, *httpsAddr, *httpAddr)
	if err := srv.Run(ctx); err != nil {
		slog.Error("fail, 服务退出", "err", err)
		os.Exit(1)
	}
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
		for _, ip := range certs.LocalIPv4s() {
			fmt.Printf("  首次使用先装证书: http://%s:%s/ca.crt\n", ip.String(), httpPort)
			break
		}
	}
	fmt.Printf("  电脑输出设备: %s\n", deviceName)
	fmt.Printf("  会议/聊天 App 的麦克风请选: %s\n", micName)
	fmt.Println("========================================================")
}
