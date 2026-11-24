package driver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/afret0/phoneMic/internal/audio"
)

const (
	DefaultMatch = "blackhole"
	driverPath   = "/Library/Audio/Plug-Ins/HAL/BlackHole2ch.driver"
	caskAPIURL   = "https://formulae.brew.sh/api/cask/blackhole-2ch.json"
)

func Installed() bool {
	_, err := os.Stat(driverPath)
	return err == nil
}

func Install() error {
	if Installed() {
		slog.Info("skip, BlackHole 已安装, 无需重复安装", "path", driverPath)
		return nil
	}

	body, err := fetchText(caskAPIURL)
	if err != nil {
		return fmt.Errorf("获取 BlackHole 最新版本信息失败: %w", err)
	}
	var cask struct {
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &cask); err != nil {
		return fmt.Errorf("解析 brew cask 信息失败: %w", err)
	}
	if cask.URL == "" || cask.SHA256 == "" || cask.SHA256 == "no_check" {
		return fmt.Errorf("brew cask 信息缺少 url/sha256, url: %s", cask.URL)
	}
	slog.Info("hit, 取得 BlackHole 官方包信息", "version", cask.Version, "url", cask.URL)

	tmpDir, err := os.MkdirTemp("", "phonemic-driver-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pkgPath := filepath.Join(tmpDir, "BlackHole2ch.pkg")
	if err := download(cask.URL, pkgPath); err != nil {
		return err
	}
	if err := verifySHA256(pkgPath, cask.SHA256); err != nil {
		return err
	}

	slog.Info("recv, 请求管理员授权安装 BlackHole, 请在系统弹窗输入密码")
	script := fmt.Sprintf("/usr/sbin/installer -pkg '%s' -target /", pkgPath)
	if err := adminRun(script); err != nil {
		return err
	}

	if !waitFor(Installed, true, 15*time.Second) {
		return fmt.Errorf("安装命令已执行但未检测到驱动文件: %s", driverPath)
	}
	if deviceLoaded() {
		slog.Info("done, BlackHole 安装完成并已加载", "path", driverPath)
		return nil
	}
	slog.Info("hit, 驱动文件已安装, 但 macOS(SIP) 不允许热重启音频服务, 需要重启电脑后 BlackHole 才会出现在设备列表", "path", driverPath)
	return nil
}

func deviceLoaded() bool {
	deadline := time.Now().Add(10 * time.Second)
	for {
		devices, err := audio.ListOutputDevices()
		if err == nil {
			for _, d := range devices {
				if strings.Contains(strings.ToLower(d.Name), DefaultMatch) {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}

func Uninstall() error {
	if !Installed() {
		slog.Info("skip, BlackHole 未安装, 无需卸载", "path", driverPath)
		return nil
	}
	slog.Info("recv, 请求管理员授权卸载 BlackHole, 请在系统弹窗输入密码")
	script := fmt.Sprintf("/bin/rm -rf '%s'", driverPath)
	if err := adminRun(script); err != nil {
		return err
	}
	if !waitFor(Installed, false, 15*time.Second) {
		return fmt.Errorf("卸载命令已执行但驱动文件仍存在: %s", driverPath)
	}
	slog.Info("done, BlackHole 驱动文件已删除, 重启电脑后设备列表才会移除它; 正在使用它的 App 需重新选择音频设备")
	return nil
}

func adminRun(shell string) error {
	cmd := exec.Command("osascript", "-e", fmt.Sprintf("do shell script %q with administrator privileges", shell))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("管理员授权或命令执行失败: %s, output: %s", err, string(out))
	}
	return nil
}

func waitFor(cond func() bool, want bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() == want {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return cond() == want
}
