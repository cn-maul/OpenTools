//go:build !windows

package crashdiag

import (
	"context"
	"errors"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return snapshot{}, errors.New("蓝屏与重启诊断当前仅支持 Windows")
}
