//go:build !windows

package uninstaller

import (
	"context"
	"errors"

	"opentools/pkg/tool"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return snapshot{}, errors.New("软件卸载当前仅支持 Windows")
}

func runUninstall(ctx context.Context, entry appEntry, report tool.Reporter) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("软件卸载当前仅支持 Windows")
}
