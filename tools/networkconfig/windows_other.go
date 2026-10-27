//go:build !windows

package networkconfig

import (
	"context"
	"errors"

	"opentools/pkg/tool"
)

func readSnapshot(context.Context) (snapshot, error) {
	return snapshot{}, errors.New("网络与 DNS 工具当前仅支持 Windows")
}

func captureBackup(context.Context, adapter) (backup, error) {
	return backup{}, errors.New("网络与 DNS 工具当前仅支持 Windows")
}

func setDNS(context.Context, int, string, string, []string, tool.Reporter) error {
	return errors.New("网络与 DNS 工具当前仅支持 Windows")
}

func restoreNetwork(context.Context, int, string, string, backup, tool.Reporter) error {
	return errors.New("网络与 DNS 工具当前仅支持 Windows")
}
