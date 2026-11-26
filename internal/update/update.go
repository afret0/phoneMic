package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/afret0/phoneMic/internal/version"
	"github.com/minio/selfupdate"
)

const (
	LatestAPI   = "https://api.github.com/repos/afret0/phoneMic/releases/latest"
	ReleasePage = "https://github.com/afret0/phoneMic/releases/latest"
)

type Asset struct {
	Name   string
	URL    string
	Size   int64
	SHA256 string
}

type release struct {
	Tag     string
	Version string
	Asset   Asset
}

type State struct {
	Busy         bool   `json:"busy"`
	Op           string `json:"op"`
	Checked      bool   `json:"checked"`
	Latest       string `json:"latest"`
	Newer        bool   `json:"newer"`
	AssetMissing bool   `json:"assetMissing"`
	Ready        bool   `json:"ready"`
	Downloaded   int64  `json:"downloaded"`
	Total        int64  `json:"total"`
	LastError    string `json:"lastError"`
}

type Manager struct {
	mu      sync.Mutex
	st      State
	rel     *release
	apiURL  string
	current string
	exePath string
	client  *http.Client
}

func NewManager() *Manager {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		slog.Error("fail, 取自身可执行文件路径失败, 应用内更新不可用", "err", err)
		exe = ""
	}
	return newManager(LatestAPI, version.Version, exe)
}

func newManager(apiURL, current, exePath string) *Manager {
	return &Manager{apiURL: apiURL, current: current, exePath: exePath, client: &http.Client{}}
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *Manager) TriggerCheck() bool { return m.trigger("check") }

func (m *Manager) TriggerDownload() bool {
	m.mu.Lock()
	rel := m.rel
	ok := rel != nil && rel.Asset.URL != "" && m.st.Newer && !m.st.Ready && m.exePath != ""
	m.mu.Unlock()
	if !ok {
		slog.Info("skip, 无可下载的更新", "已发现新版", rel != nil, "exePath", m.exePath)
		return false
	}
	return m.trigger("download")
}

func (m *Manager) trigger(op string) bool {
	m.mu.Lock()
	if m.st.Busy {
		cur := m.st.Op
		m.mu.Unlock()
		slog.Info("skip, 更新操作进行中, 忽略新请求", "进行中的操作", cur, "请求的操作", op)
		return false
	}
	m.st.Busy, m.st.Op, m.st.LastError = true, op, ""
	if op == "download" {
		m.st.Downloaded = 0
	}
	m.mu.Unlock()
	slog.Info("recv, 更新操作开始", "op", op, "当前版本", m.current)
	go m.run(op)
	return true
}

func (m *Manager) run(op string) {
	var err error
	if op == "check" {
		err = m.check()
	} else {
		err = m.download()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Busy, m.st.Op = false, ""
	if err != nil {
		m.st.LastError = err.Error()
		slog.Error("fail, 更新操作失败", "op", op, "err", err)
		return
	}
	slog.Info("done, 更新操作完成", "op", op, "latest", m.st.Latest, "newer", m.st.Newer, "ready", m.st.Ready)
}

type apiAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type apiRelease struct {
	TagName string     `json:"tag_name"`
	Assets  []apiAsset `json:"assets"`
}

func (m *Manager) check() error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "phoneMic/"+m.current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		m.mu.Lock()
		m.st.Checked, m.st.Newer, m.st.Latest, m.rel = true, false, "", nil
		m.mu.Unlock()
		slog.Info("skip, 仓库尚无 release, 视为已是最新")
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release api http %d", resp.StatusCode)
	}
	var ar apiRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ar); err != nil {
		return err
	}
	ver := strings.TrimPrefix(ar.TagName, "v")
	if parseVersion(ver) == nil {
		return fmt.Errorf("release tag 版本号不合法: %s", ar.TagName)
	}
	newer := compareVersions(ver, m.current) > 0
	asset, hasAsset := pickAsset(ar, ver, runtime.GOOS, runtime.GOARCH)
	rel := &release{Tag: ar.TagName, Version: ver, Asset: asset}
	m.mu.Lock()
	m.rel = rel
	m.st.Checked, m.st.Newer, m.st.Latest = true, newer, ar.TagName
	m.st.AssetMissing = newer && !hasAsset
	m.mu.Unlock()
	slog.Info("hit, 版本检查完成", "latest", ar.TagName, "当前版本", m.current, "newer", newer, "本平台资产", asset.Name)
	return nil
}

func platformSuffix(goos, goarch string) string {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "_mac_arm64"
	case goos == "windows" && goarch == "amd64":
		return "_win_x64.exe"
	}
	return ""
}

func pickAsset(ar apiRelease, ver, goos, goarch string) (Asset, bool) {
	suffix := platformSuffix(goos, goarch)
	if suffix == "" {
		return Asset{}, false
	}
	want := "phoneMic_" + ver + suffix
	var fallback *Asset
	for _, a := range ar.Assets {
		if a.Name != want && !strings.HasSuffix(a.Name, suffix) {
			continue
		}
		asset := Asset{Name: a.Name, URL: a.URL, Size: a.Size}
		if hexSum, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
			asset.SHA256 = hexSum
		}
		if a.Name == want {
			return asset, true
		}
		if fallback == nil {
			fallback = &asset
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return Asset{}, false
}

func (m *Manager) download() error {
	m.mu.Lock()
	rel := m.rel
	target := m.exePath
	m.mu.Unlock()
	if rel == nil || rel.Asset.URL == "" {
		return fmt.Errorf("尚未发现可下载的更新")
	}
	resp, err := m.client.Get(rel.Asset.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download http %d", resp.StatusCode)
	}
	m.mu.Lock()
	m.st.Total = rel.Asset.Size
	m.mu.Unlock()

	tmp, err := os.CreateTemp(filepath.Dir(target), ".phone_mic_update_*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), &progressReader{r: resp.Body, m: m})
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if rel.Asset.Size > 0 && written != rel.Asset.Size {
		return fmt.Errorf("下载字节数不符: 期望 %d, 实际 %d", rel.Asset.Size, written)
	}
	if rel.Asset.SHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, rel.Asset.SHA256) {
			return fmt.Errorf("sha256 校验失败: 期望 %s, 实际 %s", rel.Asset.SHA256, got)
		}
	}
	f, err := os.Open(tmpName)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := selfupdate.Apply(f, selfupdate.Options{TargetPath: target}); err != nil {
		if rerr := selfupdate.RollbackError(err); rerr != nil {
			slog.Error("fail, 替换失败且回滚失败", "target", target, "err", err, "rollbackErr", rerr)
		}
		return err
	}
	m.mu.Lock()
	m.st.Ready = true
	m.st.Downloaded = written
	m.mu.Unlock()
	slog.Info("done, 新版本已写入, 等待手动重启生效", "target", target, "version", rel.Version, "bytes", written)
	return nil
}

type progressReader struct {
	r io.Reader
	m *Manager
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.m.mu.Lock()
		p.m.st.Downloaded += int64(n)
		p.m.mu.Unlock()
	}
	return n, err
}

func (m *Manager) Restart() error {
	if m.exePath == "" {
		return fmt.Errorf("无法定位自身可执行文件")
	}
	cmd := exec.Command(m.exePath, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		slog.Error("fail, 拉起新版本进程失败", "exe", m.exePath, "err", err)
		return err
	}
	slog.Info("done, 新版本进程已拉起, 当前实例退出交接", "exe", m.exePath, "pid", cmd.Process.Pid)
	return nil
}

func OpenReleasePage() error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", ReleasePage)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", ReleasePage)
	default:
		return fmt.Errorf("本平台不支持打开浏览器: %s", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		slog.Error("fail, 打开 release 下载页失败", "url", ReleasePage, "err", err)
		return err
	}
	slog.Info("done, 已打开 release 下载页", "url", ReleasePage)
	return nil
}

func parseVersion(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		nums = append(nums, n)
	}
	return nums
}

func compareVersions(a, b string) int {
	na, nb := parseVersion(a), parseVersion(b)
	if na == nil || nb == nil {
		return 0
	}
	for i := 0; i < len(na) || i < len(nb); i++ {
		var va, vb int
		if i < len(na) {
			va = na[i]
		}
		if i < len(nb) {
			vb = nb[i]
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	return 0
}
