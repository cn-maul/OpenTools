//go:build !windows

package systemconfig

import (
	"context"
	"os"
	"runtime"
	"time"

	"opentools/pkg/tool"
)

func collect(ctx context.Context, _ tool.Reporter) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	host, _ := os.Hostname()
	return snapshot{
		SchemaVersion: 1,
		CollectedAt:   time.Now().Format(time.RFC3339),
		Computer: computerInfo{
			Name:        host,
			CurrentUser: os.Getenv("USER"),
		},
		OS: operatingSystem{
			Caption:      runtime.GOOS,
			Architecture: runtime.GOARCH,
		},
		CPU: cpuInfo{
			LogicalProcessors: runtime.NumCPU(),
		},
	}, nil
}
