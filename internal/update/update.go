package update

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/afret0/phoneMic/internal/version"
)

const releasesAPI = "https://api.github.com/repos/afret0/phoneMic/releases/latest"

type Info struct {
	Latest string `json:"latest"`
	URL    string `json:"url"`
	APK    string `json:"apk"`
	Exe    string `json:"exe"`
	Newer  bool   `json:"newer"`
}

func Check(ctx context.Context) *Info {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	if err != nil {
		slog.Info("skip, 更新检查请求构造失败", "err", err)
		return nil
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "phoneMic/"+version.Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Info("skip, 更新检查网络失败", "err", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		slog.Info("skip, 仓库尚无 Release, 更新检查跳过")
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		slog.Info("skip, 更新检查响应异常", "status", resp.StatusCode)
		return nil
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		slog.Info("skip, 更新检查解析失败", "err", err)
		return nil
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if !validVersion(latest) {
		slog.Info("skip, Release tag 不是三段版本号", "tag", rel.TagName)
		return nil
	}
	info := &Info{Latest: latest, URL: rel.HTMLURL, Newer: compareVersions(latest, version.Version) > 0}
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".apk") {
			info.APK = a.URL
		}
		if strings.HasSuffix(n, ".exe") {
			info.Exe = a.URL
		}
	}
	slog.Info("done, 更新检查完成", "current", version.Version, "latest", latest, "newer", info.Newer)
	return info
}

func validVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var na, nb int
		if i < len(pa) {
			na, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(pb[i])
		}
		if na != nb {
			if na > nb {
				return 1
			}
			return -1
		}
	}
	return 0
}
