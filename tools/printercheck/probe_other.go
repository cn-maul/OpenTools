//go:build !windows

package printercheck

import (
	"context"
	"errors"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return snapshot{}, errors.New("打印机检测工具当前仅支持 Windows")
}
