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

// Version is the current OpenTools tool contract version.
const Version = 1

// Effect describes an externally observable capability of a tool.
type Effect string

const (
	EffectReadFiles      Effect = "read-files"
	EffectWriteFiles     Effect = "write-files"
	EffectDeleteFiles    Effect = "delete-files"
	EffectNetwork        Effect = "network"
	EffectSecrets        Effect = "secrets"
	EffectSystemSettings Effect = "system-settings"
)

// Definition is the stable, serializable description of a tool.
type Definition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category"`
	Icon        string   `json:"icon,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Version     string   `json:"version"`
	Effects     []Effect `json:"effects,omitempty"`
	Input       Schema   `json:"input"`
}

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
	// WorkDir is a private, persistent directory for this invocation.
	// Artifacts returned by the tool must be written below it.
	WorkDir string

	// DataDir is the per-user OpenTools data directory. Tools should use it
	// only for small, durable state that must outlive one job.
	DataDir string
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
	Text   string       `json:"text,omitempty"`
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
		if len(section.Blocks) == 0 && strings.TrimSpace(section.Text) == "" {
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
	case strings.TrimSpace(def.Name) == "":
		return fmt.Errorf("tool %q: name is required", def.ID)
	case strings.TrimSpace(def.Category) == "":
		return fmt.Errorf("tool %q: category is required", def.ID)
	case strings.TrimSpace(def.Version) == "":
		return fmt.Errorf("tool %q: version is required", def.ID)
	}
	if def.Input.Type != "" && def.Input.Type != "object" {
		return fmt.Errorf("tool %q: top-level input must be an object", def.ID)
	}
	return nil
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
