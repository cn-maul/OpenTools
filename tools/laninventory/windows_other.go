//go:build !windows

package laninventory

import (
	"context"
	"errors"
)

func readWindowsNetwork(context.Context) (networkState, error) {
	return networkState{}, errors.New("局域网扫描仅支持 Windows")
}

func neighbors(context.Context) map[string]string { return nil }
