package single

import "testing"

func TestIsPhoneMicProcess(t *testing.T) {
	cases := []struct {
		name string
		exe  string
		want bool
	}{
		{"win 交付名", "phoneMic_0.1.4_win_x64.exe", true},
		{"win 开发名", "phone_mic.exe", true},
		{"mac 交付名", "phoneMic_0.1.5_mac_arm64", true},
		{"mac 开发名", "phone_mic", true},
		{"大小写不敏感", "PHONEMIC_0.1.5_WIN_X64.EXE", true},
		{"前缀而非包含", "myphone_mic.exe", false},
		{"相近名不误杀", "phone.exe", false},
		{"无关进程", "explorer.exe", false},
		{"空名", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPhoneMicProcess(c.exe); got != c.want {
				t.Fatalf("isPhoneMicProcess(%q) = %t, want %t", c.exe, got, c.want)
			}
		})
	}
}
