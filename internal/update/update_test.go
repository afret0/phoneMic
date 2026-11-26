package update

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"同版本", "0.1.18", "0.1.18", 0},
		{"patch 更新", "0.1.19", "0.1.18", 1},
		{"patch 更旧", "0.1.17", "0.1.18", -1},
		{"minor 更大优先于 patch", "0.2.0", "0.1.99", 1},
		{"major 更大优先于 minor", "1.0.0", "0.99.99", 1},
		{"双位数 patch 按数值比较", "0.1.100", "0.1.99", 1},
		{"缺段按零处理", "0.2", "0.2.0", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compareVersions(c.a, c.b); got != c.want {
				t.Fatalf("compare(%s, %s): %d, want %d", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestValidVersion(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0.1.18", true},
		{"1.2.3", true},
		{"v0.1.18", false},
		{"0.1", false},
		{"0.1.18-beta", false},
		{"", false},
		{"0..1", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := validVersion(c.in); got != c.want {
				t.Fatalf("validVersion(%q): %t, want %t", c.in, got, c.want)
			}
		})
	}
}
