//go:build !windows

package diskhealth

import (
	"context"
	"errors"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return snapshot{}, errors.New("磁盘健康检测当前仅支持 Windows")
}
