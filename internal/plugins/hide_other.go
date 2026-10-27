//go:build !windows

package plugins

import "os/exec"

func hideWindow(*exec.Cmd) error {
	return nil
}
