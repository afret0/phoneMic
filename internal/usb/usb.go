package usb

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Device struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
}

type State struct {
	Supported bool     `json:"supported"`
	Devices   []Device `json:"devices"`
	Ready     []string `json:"ready"`
	LastError string   `json:"lastError"`
}

type assetFile struct {
	name string
	data []byte
	exec bool
}

type Manager struct {
	dir   string
	ports []int

	mu        sync.Mutex
	supported bool
	devices   []Device
	ready     map[string]bool
	lastError string

	adbPath  string
	failSeen map[string]string
	authSeen map[string]bool
}

func NewManager(dir string, ports ...int) *Manager {
	return &Manager{
		dir:       dir,
		ports:     ports,
		supported: len(assetFiles) > 0,
		ready:     map[string]bool{},
		failSeen:  map[string]string{},
		authSeen:  map[string]bool{},
	}
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := State{Supported: m.supported, LastError: m.lastError}
	st.Devices = append(st.Devices, m.devices...)
	for serial, ok := range m.ready {
		if ok {
			st.Ready = append(st.Ready, serial)
		}
	}
	return st
}

func (m *Manager) Run(ctx context.Context) {
	if !m.supported {
		slog.Info("skip, 当前平台无内置 adb, USB 直连不可用")
		return
	}
	if err := m.extract(); err != nil {
		slog.Error("fail, 内置 adb 释放失败, USB 直连停用", "dir", m.dir, "err", err)
		m.mu.Lock()
		m.supported = false
		m.lastError = "内置 adb 释放失败: " + err.Error()
		m.mu.Unlock()
		return
	}
	slog.Info("done, USB 直连管理器已启动", "adb", m.adbPath, "ports", m.ports)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("done, USB 直连管理器退出")
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Manager) extract() error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	for _, a := range assetFiles {
		target := filepath.Join(m.dir, a.name)
		if st, err := os.Stat(target); err == nil && st.Size() == int64(len(a.data)) {
			continue
		}
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, a.data, 0o600); err != nil {
			return err
		}
		if a.exec {
			if err := os.Chmod(tmp, 0o755); err != nil {
				return err
			}
		}
		if err := os.Rename(tmp, target); err != nil {
			return err
		}
		slog.Info("done, 内置 adb 资产已释放", "file", target, "bytes", len(a.data))
	}
	m.adbPath = filepath.Join(m.dir, assetFiles[0].name)
	return nil
}

func (m *Manager) adb(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, m.adbPath, args...)
	hideWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}

func (m *Manager) logFail(key, msg string, err error) {
	m.mu.Lock()
	changed := m.failSeen[key] != err.Error()
	if changed {
		m.failSeen[key] = err.Error()
		m.lastError = msg + ": " + err.Error()
	}
	m.mu.Unlock()
	if changed {
		slog.Error("fail, "+msg, "key", key, "err", err)
	}
}

func (m *Manager) tick(ctx context.Context) {
	out, err := m.adb(ctx, "devices")
	if err != nil {
		m.logFail("devices", "adb 设备枚举失败", err)
		return
	}
	devs := parseDevices(out)

	m.mu.Lock()
	present := map[string]bool{}
	for _, d := range devs {
		present[d.Serial] = true
	}
	for serial := range m.ready {
		if !present[serial] {
			delete(m.ready, serial)
			delete(m.failSeen, serial)
			slog.Info("hit, USB 设备已断开, 隧道随设备失效", "serial", serial)
		}
	}
	for serial := range m.authSeen {
		if !present[serial] {
			delete(m.authSeen, serial)
		}
	}
	m.devices = devs
	m.lastError = ""
	m.mu.Unlock()

	for _, d := range devs {
		if d.State == "unauthorized" {
			m.mu.Lock()
			seen := m.authSeen[d.Serial]
			m.authSeen[d.Serial] = true
			m.mu.Unlock()
			if !seen {
				slog.Info("skip, 手机未授权 USB 调试, 请在手机弹窗上点允许", "serial", d.Serial)
			}
			continue
		}
		if d.State != "device" {
			continue
		}
		m.ensureTunnel(ctx, d.Serial)
	}
}

func (m *Manager) ensureTunnel(ctx context.Context, serial string) {
	listOut, err := m.adb(ctx, "-s", serial, "reverse", "--list")
	if err != nil {
		m.logFail(serial, "读取 USB 隧道列表失败", err)
		m.setReady(serial, false)
		return
	}
	missing := missingPorts(listOut, m.ports)
	if len(missing) == 0 {
		if m.setReady(serial, true) {
			slog.Info("done, USB 直连隧道已就绪", "serial", serial, "ports", m.ports)
		}
		return
	}
	for _, p := range missing {
		spec := fmt.Sprintf("tcp:%d", p)
		if _, err := m.adb(ctx, "-s", serial, "reverse", spec, spec); err != nil {
			m.logFail(serial, "建立 USB 隧道失败", err)
			m.setReady(serial, false)
			return
		}
	}
	m.mu.Lock()
	delete(m.failSeen, serial)
	m.mu.Unlock()
	m.setReady(serial, true)
	slog.Info("done, USB 直连隧道已建立", "serial", serial, "ports", missing)
}

func (m *Manager) setReady(serial string, ok bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := m.ready[serial] != ok
	if ok {
		m.ready[serial] = true
	} else {
		delete(m.ready, serial)
	}
	return changed && ok
}

func parseDevices(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			devs = append(devs, Device{Serial: fields[0], State: fields[1]})
		}
	}
	return devs
}

func missingPorts(listOut string, ports []int) []int {
	var missing []int
	for _, p := range ports {
		token := fmt.Sprintf("tcp:%d", p)
		found := false
		for _, line := range strings.Split(listOut, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[1] == token && fields[2] == token {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, p)
		}
	}
	return missing
}
