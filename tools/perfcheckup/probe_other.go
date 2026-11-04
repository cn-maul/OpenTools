//go:build !windows

package perfcheckup

import (
	"context"
	"errors"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return snapshot{}, errors.New("卡慢体检当前仅支持 Windows")
}
