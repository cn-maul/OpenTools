//go:build !windows

package elevation

import (
	"context"
	"errors"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

// runElevated is Windows-only. Other platforms have no UAC; a tool that asks
// for elevation gets a clear error instead of a silently non-elevated run.
func runElevated(context.Context, tool.ElevateRequest) (tool.ElevateResult, error) {
	return tool.ElevateResult{}, errors.New("this platform has no elevation service")
}
