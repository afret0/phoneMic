package driver

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/afret0/phoneMic/internal/audio"
)

const (
	DefaultMatch     = "cable input"
	cablePageURL     = "https://vb-audio.com/Cable/index.htm"
	cableFallbackURL = "https://download.vb-audio.com/Download_CABLE/VBCABLE_Driver_Pack46.zip"
)

var cablePackRe = regexp.MustCompile(`https://download\.vb-audio\.com/Download_CABLE/VBCABLE_Driver_Pack\d+\.zip`)

func Installed() bool {
	devices, err := audio.ListOutputDevices()
	if err != nil {
		slog.Error("fail, 枚举输出设备失败, 按未安装处理", "err", err)
		return false
	}
	return findCable(devices)
}

func findCable(devices []audio.OutputDevice) bool {
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name), DefaultMatch) {
			return true
		}
	}
	return false
}

func Probe() (installed, loaded bool) {
	devices, err := audio.ListOutputDevices()
	if err != nil {
		slog.Error("fail, 枚举输出设备失败, 按未安装处理", "err", err)
		return false, false
	}
	found := findCable(devices)
	return found, found
}

func Install() error {
	if Installed() {
		slog.Info("skip, VB-Cable 已安装, 无需重复安装")
		return nil
	}
	setupPath, cleanup, err := fetchSetup()
	if err != nil {
		return err
	}
	defer cleanup()

	slog.Info("recv, 请求管理员授权安装 VB-Cable, 请在 UAC 弹窗确认")
	if err := runElevated(setupPath, "-i", "-h"); err != nil {
		return err
	}
	if !waitFor(Installed, true, 30*time.Second) {
		return fmt.Errorf("安装命令已执行但未检测到 CABLE Input 设备, 请重启系统后重试")
	}
	slog.Info("done, VB-Cable 安装完成, 会议 App 麦克风请选 CABLE Output")
	return nil
}

func Uninstall() error {
	if !Installed() {
		slog.Info("skip, VB-Cable 未安装, 无需卸载")
		return nil
	}
	setupPath, cleanup, err := fetchSetup()
	if err != nil {
		return err
	}
	defer cleanup()

	slog.Info("recv, 请求管理员授权卸载 VB-Cable, 请在 UAC 弹窗确认")
	if err := runElevated(setupPath, "-u", "-h"); err != nil {
		return err
	}
	if !waitFor(Installed, false, 30*time.Second) {
		return fmt.Errorf("卸载命令已执行但仍检测到 CABLE Input 设备, 请重启系统后确认")
	}
	slog.Info("done, VB-Cable 已卸载, 正在使用它的 App 需重新选择音频设备")
	return nil
}

func fetchSetup() (string, func(), error) {
	url := cableFallbackURL
	if page, err := fetchText(cablePageURL); err == nil {
		if m := cablePackRe.FindString(page); m != "" {
			url = m
		}
	} else {
		slog.Info("hit, 官网页面获取失败, 使用固定版本安装包", "err", err, "url", url)
	}

	tmpDir, err := os.MkdirTemp("", "phonemic-driver-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	zipPath := filepath.Join(tmpDir, "vbcable.zip")
	if err := download(url, zipPath); err != nil {
		cleanup()
		return "", nil, err
	}
	setupPath, err := extractSetup(zipPath, tmpDir)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return setupPath, cleanup, nil
}

func extractSetup(zipPath, dstDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var setupPath string
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || name == "" {
			continue
		}
		dst := filepath.Join(dstDir, name)
		if err := extractFile(f, dst); err != nil {
			return "", err
		}
		lower := strings.ToLower(name)
		if lower == "vbcable_setup_x64.exe" {
			setupPath = dst
		} else if lower == "vbcable_setup.exe" && setupPath == "" {
			setupPath = dst
		}
	}
	if setupPath == "" {
		return "", fmt.Errorf("安装包内未找到 VBCABLE_Setup_x64.exe")
	}
	return setupPath, nil
}

func extractFile(f *zip.File, dst string) error {
	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return fmt.Errorf("extract %s: %w", f.Name, err)
	}
	return nil
}

func runElevated(exePath string, args ...string) error {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, "'"+strings.ReplaceAll(a, "'", "''")+"'")
	}
	ps := fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs -Wait",
		strings.ReplaceAll(exePath, "'", "''"), strings.Join(quoted, ","))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("UAC 授权或命令执行失败: %s, output: %s", err, string(out))
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
