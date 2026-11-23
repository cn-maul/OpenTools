// Package elevation runs one command with administrator rights.
//
// The host owns the UAC prompt; a tool only receives the exit status and
// captured output. This is a system capability, not a UI surface: it does
// not depend on any GUI toolkit, so a headless host can grant it too.
package elevation

import (
	"context"
	"errors"
	"strings"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

// ErrUnsupported is returned on platforms without an elevation service.
var ErrUnsupported = errors.New("this platform has no elevation service")

// Runner executes elevation requests. The desktop build implements it with
// the Windows runas verb; a stub returns ErrUnsupported elsewhere.
type Runner interface {
	Run(ctx context.Context, request tool.ElevateRequest) (tool.ElevateResult, error)
}

// Run executes one elevation request, validating it first.
func Run(ctx context.Context, request tool.ElevateRequest) (tool.ElevateResult, error) {
	if strings.TrimSpace(request.Program) == "" {
		return tool.ElevateResult{}, errors.New("elevation request has no program")
	}
	return runElevated(ctx, request)
}
