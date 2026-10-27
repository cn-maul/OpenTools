//go:build windows

package plugins

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	return nil
}
