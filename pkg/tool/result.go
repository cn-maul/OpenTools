package tool

import (
	"fmt"
	"strings"
)

// BlockKind is a closed set of renderable result blocks.
type BlockKind string

const (
	BlockText   BlockKind = "text"
	BlockNotice BlockKind = "notice"
	BlockCode   BlockKind = "code"
	BlockJSON   BlockKind = "json"
	BlockTable  BlockKind = "table"
	BlockDiff   BlockKind = "diff"
	BlockGrid   BlockKind = "status-grid"
	BlockMeter  BlockKind = "meter"
	BlockFacts  BlockKind = "facts"
)

// Tone conveys status without dictating a component.
type Tone string

const (
	ToneNeutral Tone = "neutral"
	ToneSuccess Tone = "success"
	ToneWarning Tone = "warning"
	ToneDanger  Tone = "danger"
)

// Result is the structured output of one invocation.
type Result struct {
	Summary   string     `json:"summary"`
	Blocks    []Block    `json:"blocks,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Actions   []Action   `json:"actions,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
}

// Block is one piece of rendered output. Data is only used by BlockJSON.
type Block struct {
	Kind     BlockKind  `json:"kind"`
	Title    string     `json:"title,omitempty"`
	Tone     Tone       `json:"tone,omitempty"`
	Text     string     `json:"text,omitempty"`
	Language string     `json:"language,omitempty"`
	Data     any        `json:"data,omitempty"`
	Columns  []string   `json:"columns,omitempty"`
	Rows     [][]string `json:"rows,omitempty"`
}

// MeterItem is one progress ring or bar. Value is in the unit named by Unit,
// normally 0-100 for percentages.
type MeterItem struct {
	Label    string `json:"label"`
	Value    float64 `json:"value"`
	Max      float64 `json:"max,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Tone     Tone   `json:"tone,omitempty"`
	Variant  string `json:"variant,omitempty"`
	Segments []MeterSegment `json:"segments,omitempty"`
}

// MeterSegment is an optional colored range inside a bar, used for
// multi-part capacity such as several disks in one volume group.
type MeterSegment struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Tone  Tone    `json:"tone,omitempty"`
}

// Fact is one labelled value. Group renders a visual section heading.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Group string `json:"group,omitempty"`
}

// StatusCell is one compact grid cell rendered by the status-grid block.
// Empty Tone renders as a muted idle state.
type StatusCell struct {
	Label    string `json:"label"`
	Tone     Tone   `json:"tone,omitempty"`
	Category string `json:"category,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// StatusLegendItem maps one tone to a human-readable grid legend label.
type StatusLegendItem struct {
	Label    string `json:"label"`
	Tone     Tone   `json:"tone,omitempty"`
	Category string `json:"category,omitempty"`
}

// Artifact is a file produced in the job's private work directory.
type Artifact struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	MIME string `json:"mime,omitempty"`
}

// Action is a follow-up invocation of the tool that produced the result.
//
// Tools cannot use an action to invoke another tool or pass arbitrary host
// commands: Input is validated against the same tool schema as a normal run.
// Confirm is displayed before execution and should describe the side effect.
type Action struct {
	Label   string         `json:"label"`
	Tone    Tone           `json:"tone,omitempty"`
	Input   map[string]any `json:"input"`
	Confirm string         `json:"confirm,omitempty"`
}

// ValidateResult checks the transport-neutral result contract. Artifact
// paths are validated separately by the host against the job work directory.
func ValidateResult(result Result) error {
	if strings.TrimSpace(result.Summary) == "" {
		return fmt.Errorf("tool result summary is required")
	}
	if err := validateBlocks(result.Blocks); err != nil {
		return err
	}
	for i, action := range result.Actions {
		if strings.TrimSpace(action.Label) == "" {
			return fmt.Errorf("result action %d has no label", i)
		}
		if action.Input == nil {
			return fmt.Errorf("result action %d has no input", i)
		}
		switch action.Tone {
		case "", ToneNeutral, ToneSuccess, ToneWarning, ToneDanger:
		default:
			return fmt.Errorf("result action %d has unknown tone %q", i, action.Tone)
		}
	}
	return nil
}

func validateBlocks(blocks []Block) error {
	for i, block := range blocks {
		switch block.Kind {
		case BlockText, BlockNotice, BlockCode, BlockJSON, BlockTable, BlockDiff, BlockGrid, BlockMeter, BlockFacts:
		default:
			return fmt.Errorf("result block %d has unknown kind %q", i, block.Kind)
		}
		switch block.Tone {
		case "", ToneNeutral, ToneSuccess, ToneWarning, ToneDanger:
		default:
			return fmt.Errorf("result block %d has unknown tone %q", i, block.Tone)
		}
	}
	return nil
}
