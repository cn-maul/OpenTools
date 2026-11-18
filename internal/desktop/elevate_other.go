//go:build !windows

package desktop

import (
	"context"
	"errors"

	"github.com/cn-maul/OpenTools/pkg/tool"
)

// runElevated is Windows-only. Other platforms have no UAC; a tool that asks
// for elevation gets a clear error instead of a silently non-elevated run.
func runElevated(context.Context, tool.ElevateRequest) (tool.ElevateResult, error) {
	return tool.ElevateResult{}, errors.New("this platform has no elevation service")
}
