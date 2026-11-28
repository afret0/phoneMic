//go:build !windows

package usb

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
