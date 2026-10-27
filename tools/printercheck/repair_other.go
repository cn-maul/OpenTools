//go:build !windows

package printercheck

import (
	"context"
	"errors"

	"opentools/pkg/tool"
)

func applyRepair(ctx context.Context, item repair, report tool.Reporter) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("打印机修复功能当前仅支持 Windows")
}

func windowsPowerShell() (string, error) {
	return "", errors.New("打印机修复功能当前仅支持 Windows")
}
