//go:build windows

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledSetup_解出官方安装包(t *testing.T) {
	setupPath, cleanup, err := bundledSetup()
	if err != nil {
		t.Fatalf("bundledSetup err: %s", err)
	}
	defer cleanup()
	if strings.ToLower(filepath.Base(setupPath)) != "vbcable_setup_x64.exe" {
		t.Fatalf("setup 文件名不对: %s", setupPath)
	}
	st, err := os.Stat(setupPath)
	if err != nil {
		t.Fatalf("setup 不可用, err: %s", err)
	}
	if st.Size() == 0 {
		t.Fatal("setup 为空文件")
	}
	dir := filepath.Dir(setupPath)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup 未清理临时目录")
	}
}