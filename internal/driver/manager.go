package driver

import (
	"log/slog"
	"runtime"
	"sync"
)

type State struct {
	Installed   bool   `json:"installed"`
	Loaded      bool   `json:"loaded"`
	Busy        bool   `json:"busy"`
	Op          string `json:"op"`
	NeedsReboot bool   `json:"needsReboot"`
	LastError   string `json:"lastError"`
}

type Manager struct {
	mu          sync.Mutex
	busy        bool
	op          string
	needsReboot bool
	lastErr     string
}

func NewManager() *Manager { return &Manager{} }

func (m *Manager) State() State {
	m.mu.Lock()
	st := State{Busy: m.busy, Op: m.op, NeedsReboot: m.needsReboot, LastError: m.lastErr}
	m.mu.Unlock()
	st.Installed, st.Loaded = Probe()
	return st
}

func (m *Manager) TriggerInstall() bool   { return m.trigger("install") }
func (m *Manager) TriggerUninstall() bool { return m.trigger("uninstall") }

func (m *Manager) trigger(op string) bool {
	m.mu.Lock()
	if m.busy {
		cur := m.op
		m.mu.Unlock()
		slog.Info("skip, 驱动操作进行中, 忽略新请求", "进行中的操作", cur, "请求的操作", op)
		return false
	}
	m.busy, m.op, m.lastErr = true, op, ""
	m.mu.Unlock()
	slog.Info("recv, 驱动操作开始", "op", op)
	go m.run(op)
	return true
}

func (m *Manager) run(op string) {
	var err error
	if op == "install" {
		err = Install()
	} else {
		err = Uninstall()
	}
	installed, loaded := Probe()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.busy, m.op = false, ""
	if err != nil {
		m.lastErr = err.Error()
		slog.Error("fail, 驱动操作失败", "op", op, "err", err)
		return
	}
	if op == "install" {
		m.needsReboot = installed && !loaded
		if m.needsReboot {
			slog.Info("hit, 驱动已安装但设备未加载, 需重启电脑", "os", runtime.GOOS, "installed", installed, "loaded", loaded)
		}
	} else {
		m.needsReboot = false
	}
	slog.Info("done, 驱动操作完成", "op", op, "installed", installed, "loaded", loaded)
}