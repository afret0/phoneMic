package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"0.1.20", []int{0, 1, 20}},
		{"1.0", []int{1, 0}},
		{"7", []int{7}},
		{"", nil},
		{"0..1", nil},
		{"a.b", nil},
		{"1.x", nil},
		{"-1.2", nil},
		{"1.2.", nil},
	}
	for _, c := range cases {
		got := parseVersion(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("parseVersion(%q) = %+v, want nil", c.in, got)
			}
			continue
		}
		if got == nil || len(got) != len(c.want) {
			t.Errorf("parseVersion(%q) = %+v, want %+v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseVersion(%q) = %+v, want %+v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.21", "0.1.20", 1},
		{"0.1.20", "0.1.21", -1},
		{"0.1.20", "0.1.20", 0},
		{"0.2.0", "0.1.99", 1},
		{"1.0.0", "0.99.99", 1},
		{"0.1", "0.1.0", 0},
		{"0.1.1", "0.1", 1},
		{"0.10.0", "0.9.0", 1},
		{"bad", "0.1.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPickAsset(t *testing.T) {
	suffix := platformSuffix(runtime.GOOS, runtime.GOARCH)
	if suffix == "" {
		t.Skip("测试平台无发布资产命名")
	}
	exact := "phoneMic_0.2.0" + suffix
	other := "phoneMic_0.1.0" + suffix
	mk := func(name string) apiRelease {
		return apiRelease{TagName: "v0.2.0", Assets: []apiAsset{{Name: name, URL: "u", Size: 5, Digest: "sha256:ABCD"}}}
	}
	a, ok := pickAsset(mk(exact), "0.2.0", runtime.GOOS, runtime.GOARCH)
	if !ok || a.Name != exact || a.SHA256 != "ABCD" {
		t.Fatalf("exact match failed: %+v ok=%t", a, ok)
	}
	a, ok = pickAsset(mk(other), "0.2.0", runtime.GOOS, runtime.GOARCH)
	if !ok || a.Name != other {
		t.Fatalf("suffix fallback failed: %+v ok=%t", a, ok)
	}
	if _, ok := pickAsset(mk("phoneMic_0.2.0_android_debug.apk"), "0.2.0", runtime.GOOS, runtime.GOARCH); ok {
		t.Fatal("foreign asset should not match")
	}
	if _, ok := pickAsset(mk(exact), "0.2.0", "linux", "amd64"); ok {
		t.Fatal("unsupported platform should have no asset")
	}
	ar := apiRelease{TagName: "v0.2.0", Assets: []apiAsset{{Name: other, URL: "old"}, {Name: exact, URL: "new"}}}
	if a, ok := pickAsset(ar, "0.2.0", runtime.GOOS, runtime.GOARCH); !ok || a.URL != "new" {
		t.Fatalf("exact should win over fallback: %+v", a)
	}
}

func waitIdle(t *testing.T, m *Manager) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := m.State()
		if !st.Busy {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("操作未在期限内完成")
	return State{}
}

func newTarget(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "phone_mic_target")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func testServer(t *testing.T, tag, assetName string, payload []byte, declaredSize int64, digest string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if tag == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q,"size":%d,"digest":%q}]}`,
			tag, assetName, srv.URL+"/asset", declaredSize, digest)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckAndDownload(t *testing.T) {
	payload := []byte("new-binary-bytes-0123456789")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	assetName := "phoneMic_0.2.0" + platformSuffix(runtime.GOOS, runtime.GOARCH)
	srv := testServer(t, "v0.2.0", assetName, payload, int64(len(payload)), digest)
	target := newTarget(t, "old-binary")
	m := newManager(srv.URL+"/latest", "0.1.20", target)

	if !m.TriggerCheck() {
		t.Fatal("TriggerCheck 应启动")
	}
	st := waitIdle(t, m)
	if !st.Checked || !st.Newer || st.Latest != "v0.2.0" || st.LastError != "" || st.AssetMissing {
		t.Fatalf("check 状态不符: %+v", st)
	}
	if !m.TriggerDownload() {
		t.Fatal("TriggerDownload 应启动")
	}
	st = waitIdle(t, m)
	if !st.Ready || st.LastError != "" || st.Downloaded != int64(len(payload)) {
		t.Fatalf("download 状态不符: %+v", st)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("目标文件未替换: %q err=%s", got, err)
	}
	if m.TriggerDownload() {
		t.Fatal("已就绪后不应再次下载")
	}
}

func TestCheckNoRelease(t *testing.T) {
	srv := testServer(t, "", "", nil, 0, "")
	m := newManager(srv.URL+"/latest", "0.1.20", newTarget(t, "old"))
	m.TriggerCheck()
	st := waitIdle(t, m)
	if !st.Checked || st.Newer || st.LastError != "" {
		t.Fatalf("404 应视为已是最新: %+v", st)
	}
	if m.TriggerDownload() {
		t.Fatal("无 release 不应可下载")
	}
}

func TestCheckSameVersion(t *testing.T) {
	assetName := "phoneMic_0.1.20" + platformSuffix(runtime.GOOS, runtime.GOARCH)
	srv := testServer(t, "v0.1.20", assetName, []byte("x"), 1, "")
	m := newManager(srv.URL+"/latest", "0.1.20", newTarget(t, "old"))
	m.TriggerCheck()
	st := waitIdle(t, m)
	if !st.Checked || st.Newer || st.LastError != "" {
		t.Fatalf("同版本应不提示更新: %+v", st)
	}
}

func TestCheckAssetMissing(t *testing.T) {
	srv := testServer(t, "v0.2.0", "phoneMic_0.2.0_android_debug.apk", []byte("x"), 1, "")
	m := newManager(srv.URL+"/latest", "0.1.20", newTarget(t, "old"))
	m.TriggerCheck()
	st := waitIdle(t, m)
	if !st.Newer || !st.AssetMissing {
		t.Fatalf("缺本平台资产应标记: %+v", st)
	}
	if m.TriggerDownload() {
		t.Fatal("缺资产不应可下载")
	}
}

func TestDownloadSizeMismatch(t *testing.T) {
	payload := []byte("short")
	assetName := "phoneMic_0.2.0" + platformSuffix(runtime.GOOS, runtime.GOARCH)
	srv := testServer(t, "v0.2.0", assetName, payload, 999, "")
	target := newTarget(t, "old-binary")
	m := newManager(srv.URL+"/latest", "0.1.20", target)
	m.TriggerCheck()
	waitIdle(t, m)
	m.TriggerDownload()
	st := waitIdle(t, m)
	if st.Ready || st.LastError == "" {
		t.Fatalf("字节数不符应失败: %+v", st)
	}
	if got, _ := os.ReadFile(target); string(got) != "old-binary" {
		t.Fatalf("失败时目标文件不应被改动: %q", got)
	}
}

func TestDownloadDigestMismatch(t *testing.T) {
	payload := []byte("payload")
	assetName := "phoneMic_0.2.0" + platformSuffix(runtime.GOOS, runtime.GOARCH)
	srv := testServer(t, "v0.2.0", assetName, payload, int64(len(payload)), "sha256:deadbeef")
	target := newTarget(t, "old-binary")
	m := newManager(srv.URL+"/latest", "0.1.20", target)
	m.TriggerCheck()
	waitIdle(t, m)
	m.TriggerDownload()
	st := waitIdle(t, m)
	if st.Ready || st.LastError == "" {
		t.Fatalf("sha256 不符应失败: %+v", st)
	}
	if got, _ := os.ReadFile(target); string(got) != "old-binary" {
		t.Fatalf("失败时目标文件不应被改动: %q", got)
	}
}

func TestTriggerDownloadWithoutCheck(t *testing.T) {
	m := newManager("http://127.0.0.1:0/latest", "0.1.20", newTarget(t, "old"))
	if m.TriggerDownload() {
		t.Fatal("未检查前不应可下载")
	}
}
