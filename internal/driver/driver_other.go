//go:build !darwin && !windows

package driver

import "fmt"

const DefaultMatch = ""

func Installed() bool { return false }

func Install() error {
	return fmt.Errorf("当前系统暂不支持自动安装虚拟声卡, 请用 --device 指定已有虚拟设备")
}

func Uninstall() error {
	return fmt.Errorf("当前系统暂不支持自动卸载虚拟声卡")
}
