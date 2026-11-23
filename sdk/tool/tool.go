// Package tool defines the contract between OpenTools and its tools.
//
// This package deliberately depends only on the Go standard library. A tool
// can therefore be compiled into the desktop app, called by the CLI, or later
// hosted behind a process or HTTP boundary without importing GUI code.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Definition is the stable, serializable description of a tool.
type Definition struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Description is the long form shown in a tooltip, while Summary is the
	// single line rendered in the sidebar and header.
	Description string `json:"description,omitempty"`
	Category    string `json:"category"`
	// Icon is optional and fully self-describing: "svg:<path>|<path>" holds
	// one or more SVG path definitions rendered by the host in a 24x24
	// viewBox. Any other value renders a letter mark, so tools never rely on
	// host-side icon tables.
	Icon     string   `json:"icon,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Version  string   `json:"version"`
	// Layout selects how the host groups a tool's result sections. "tabs"
	// puts them behind a tab strip instead of stacking every section; any
	// other value keeps the default stacked view. Tools own this choice so
	// the host never needs a table of tool IDs.
	Layout string `json:"layout,omitempty"`
	// Requires lists the host services the tool calls. The host grants the
	// intersection of this list and what it implements; a call to anything
	// else is rejected.
	Requires []Capability `json:"requires,omitempty"`
	Input    Schema       `json:"input"`
	// TimeoutSeconds caps one invocation. Zero means no tool-specific limit;
	// the host may still apply its own budget.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

// Capability names one host service a tool may request.
type Capability string

const (
	CapabilitySecrets Capability = "secrets"
	CapabilityConfig  Capability = "config"
	CapabilityDialogs Capability = "dialogs"
	CapabilityNotify  Capability = "notify"
	CapabilityElevate Capability = "elevate"
)

// Input holds the JSON-shaped arguments of one invocation.
type Input struct {
	Values map[string]any `json:"values"`
}

// Decode decodes the input into a tool-specific parameter struct.
func (in Input) Decode(dst any) error {
	data, err := json.Marshal(in.Values)
	if err != nil {
		return fmt.Errorf("encode tool input: %w", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode tool input: %w", err)
	}
	return nil
}

// Environment contains host services explicitly granted to a tool.
//
// No GUI or global application state is exposed. Additional capabilities
// should be added here only when more than one tool needs them.
type Environment struct {
	// WorkDir is a private directory for this invocation. It is removed with
	// the job history, so it is not durable storage. Artifacts returned by the
	// tool must be written below it.
	WorkDir string

	// DataDir is a private, per-tool directory that survives across jobs.
	// Use it only for small, durable state; secrets and settings belong in
	// the host services.
	DataDir string

	// Host answers host service calls. It is nil when the tool runs without
	// a host (for example a standalone test). A tool should treat a nil Host
	// as "no capabilities granted" and degrade instead of failing.
	Host Host
}

// Host is the tool-side view of the host services granted to this run.
//
// Call is the only primitive: it sends a host/* request and decodes the
// result. Typed helpers live in github.com/cn-maul/OpenTools/sdk/tool/host.
// A call for a capability the host did not grant fails with
// pluginproto.CodeCapabilityDenied.
type Host interface {
	Call(ctx context.Context, method string, params, result any) error
	// Granted reports whether one capability is available.
	Granted(capability Capability) bool
}

// HostServices is the host-side dispatch point for tool-initiated calls.
//
// It is deliberately one method: the process adapter forwards a request and
// the application layer decides what it means. That keeps new services from
// changing the plugin protocol or this package.
type HostServices interface {
	Handle(ctx context.Context, toolID, method string, params json.RawMessage) (any, error)
}

// CapabilityProvider is implemented by host-service dispatchers that can
// report the capabilities they actually serve. Host process adapters use it
// to grant tools only the intersection of what they declare and what the
// host implements. Dispatchers without this interface grant nothing.
type CapabilityProvider interface {
	Capabilities() []Capability
}

// ElevateRequest describes one command a tool asks the host to run with
// administrator rights. The host owns the UAC prompt and the process launch;
// the tool only receives the exit status and captured output.
type ElevateRequest struct {
	// Title is shown in the confirmation prompt.
	Title string `json:"title"`
	// Program is the executable to run, normally powershell.exe.
	Program string `json:"program"`
	// Args are passed verbatim.
	Args []string `json:"args,omitempty"`
	// Script is an optional script the host writes to a temporary file and
	// runs, which avoids quoting problems with long or non-ASCII arguments.
	Script string `json:"script,omitempty"`
}

// ElevateResult is the outcome of an elevated command.
type ElevateResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// Reporter receives progress and log messages for a running invocation.
type Reporter interface {
	Log(level, message string)
	Progress(done, total int64)
	Section(section Section)
}

// SectionState is the lifecycle of one streamed result section.
type SectionState string

const (
	SectionPending SectionState = "pending"
	SectionRunning SectionState = "running"
	SectionReady   SectionState = "ready"
	SectionSkipped SectionState = "skipped"
)

// Section is a stable slot in the result view. Tools can publish every slot
// up front and fill them in as work completes, so the UI never jumps between
// layouts while a scan is running.
type Section struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	State  SectionState `json:"state"`
	Blocks []Block      `json:"blocks,omitempty"`
}

// ValidateSection checks one streamed section.
func ValidateSection(section Section) error {
	if strings.TrimSpace(section.ID) == "" {
		return errors.New("result section id is required")
	}
	if strings.TrimSpace(section.Title) == "" {
		return fmt.Errorf("result section %q: title is required", section.ID)
	}
	switch section.State {
	case SectionPending, SectionRunning, SectionReady, SectionSkipped:
	default:
		return fmt.Errorf("result section %q has unknown state %q", section.ID, section.State)
	}
	if section.State == SectionReady {
		if len(section.Blocks) == 0 {
			return fmt.Errorf("result section %q is ready but has no content", section.ID)
		}
		if err := validateBlocks(section.Blocks); err != nil {
			return fmt.Errorf("result section %q: %w", section.ID, err)
		}
	}
	return nil
}

// Tool is the unit of work hosted by OpenTools.
//
// Run must honor ctx cancellation and should return promptly when it is
// canceled. Implementations do not need to synchronize with a UI: the host
// owns job state and persistence.
type Tool interface {
	Definition() Definition
	Run(ctx context.Context, input Input, env Environment, report Reporter) (Result, error)
}

// ValidateDefinition checks the invariants every tool must satisfy.
func ValidateDefinition(def Definition) error {
	switch {
	case strings.TrimSpace(def.ID) == "":
		return errors.New("tool id is required")
	case !validID(def.ID):
		return fmt.Errorf(
			"tool %q: id may only contain lowercase letters, digits, '.', '-' and '_'",
			def.ID,
		)
	case strings.TrimSpace(def.Name) == "":
		return fmt.Errorf("tool %q: name is required", def.ID)
	case strings.TrimSpace(def.Summary) == "":
		return fmt.Errorf("tool %q: summary is required", def.ID)
	case strings.TrimSpace(def.Category) == "":
		return fmt.Errorf("tool %q: category is required", def.ID)
	case strings.TrimSpace(def.Version) == "":
		return fmt.Errorf("tool %q: version is required", def.ID)
	}
	if def.Input.Type != "" && def.Input.Type != "object" {
		return fmt.Errorf("tool %q: top-level input must be an object", def.ID)
	}
	if err := ValidateSchema(def.Input); err != nil {
		return fmt.Errorf("tool %q: %w", def.ID, err)
	}
	if def.TimeoutSeconds < 0 {
		return fmt.Errorf("tool %q: timeout seconds cannot be negative", def.ID)
	}
	if def.Layout != "" && def.Layout != "tabs" {
		return fmt.Errorf("tool %q: layout must be empty or %q", def.ID, "tabs")
	}
	for _, capability := range def.Requires {
		switch capability {
		case CapabilitySecrets, CapabilityConfig, CapabilityDialogs, CapabilityNotify, CapabilityElevate:
		default:
			return fmt.Errorf("tool %q: unknown capability %q", def.ID, capability)
		}
	}
	return nil
}

func validID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z',
			char >= '0' && char <= '9',
			char == '.', char == '-', char == '_':
		default:
			return false
		}
	}
	return true
}

// ResolveArtifact validates that path is a regular file inside env.WorkDir
// and returns its absolute path and size.
func ResolveArtifact(env Environment, path string) (absolute string, size int64, err error) {
	if strings.TrimSpace(path) == "" {
		return "", 0, errors.New("artifact path is empty")
	}
	root, err := filepath.Abs(env.WorkDir)
	if err != nil {
		return "", 0, fmt.Errorf("resolve work directory: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", 0, fmt.Errorf("resolve artifact: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", 0, errors.New("artifact is outside the job work directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("artifact is not a regular file")
	}
	return path, info.Size(), nil
}
