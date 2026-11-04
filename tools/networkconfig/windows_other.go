//go:build !windows

package networkconfig

import (
	"context"
	"errors"

	"opentools/pkg/tool"
)

func readSnapshot(context.Context) (snapshot, error) {
	return snapshot{}, errors.New("DNS 测速工具当前仅支持 Windows")
}

func captureBackup(context.Context, adapter) (backup, error) {
	return backup{}, errors.New("DNS 测速工具当前仅支持 Windows")
}

func setDNS(context.Context, int, string, string, []string, tool.Reporter) error {
	return errors.New("DNS 测速工具当前仅支持 Windows")
}
