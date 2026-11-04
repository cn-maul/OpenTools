//go:build !windows

package startupmgr

import (
	"context"
	"errors"

	"opentools/pkg/tool"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return snapshot{}, errors.New("启动项管理当前仅支持 Windows")
}

func runToggle(ctx context.Context, job toggleJob, report tool.Reporter) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("启动项管理当前仅支持 Windows")
}
