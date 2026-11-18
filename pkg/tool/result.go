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
	Kind     BlockKind `json:"kind"`
	Title    string    `json:"title,omitempty"`
	Tone     Tone      `json:"tone,omitempty"`
	Text     string    `json:"text,omitempty"`
	Language string    `json:"language,omitempty"`
	Data     any       `json:"data,omitempty"`
	Columns  []string  `json:"columns,omitempty"`
	// ColumnWidths optionally assigns a CSS width to each data column.
	// The renderer appends its own width for the row-action column.
	ColumnWidths []string   `json:"columnWidths,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	// RowIcons optionally aligns one data-URI icon with each table row.
	// Empty entries fall back to a text avatar in the renderer.
	RowIcons []string `json:"rowIcons,omitempty"`
	// RowActions attaches follow-up buttons to table rows. Entry i belongs
	// to Rows[i] and must carry the complete input for the follow-up run.
	RowActions [][]RowAction `json:"rowActions,omitempty"`
	// BatchAction turns the table into a multi-select list. The renderer
	// collects ItemKey from every selected row action and sends those values
	// to ItemsKey on the single batch action.
	BatchAction *BatchAction `json:"batchAction,omitempty"`
}

// BatchAction is one action applied to multiple selected table rows.
type BatchAction struct {
	Label string `json:"label"`
	Tone  Tone   `json:"tone,omitempty"`
	// Confirm may contain {count}, which the renderer replaces with the
	// number of selected rows.
	Confirm  string         `json:"confirm,omitempty"`
	Input    map[string]any `json:"input"`
	ItemKey  string         `json:"itemKey,omitempty"`
	ItemsKey string         `json:"itemsKey,omitempty"`
}

// RowAction is one button rendered inside a table row. Clicking it starts
// the follow-up invocation with Input, subject to the same schema
// validation as a normal action.
type RowAction struct {
	Label   string         `json:"label"`
	Tone    Tone           `json:"tone,omitempty"`
	Confirm string         `json:"confirm,omitempty"`
	Input   map[string]any `json:"input"`
	// Variant switches the rendering: "switch" renders a toggle that
	// reflects On, while "select" renders a compact choice menu.
	Variant string `json:"variant,omitempty"`
	On      bool   `json:"on,omitempty"`
	// Value is the selected option for a select action.
	Value   string            `json:"value,omitempty"`
	Options []RowActionOption `json:"options,omitempty"`
}

// RowActionOption is one choice in a select-style row action.
type RowActionOption struct {
	Label   string         `json:"label"`
	Value   string         `json:"value"`
	Input   map[string]any `json:"input"`
	Confirm string         `json:"confirm,omitempty"`
}

// MeterItem is one progress ring or bar. Value is in the unit named by Unit,
// normally 0-100 for percentages.
type MeterItem struct {
	Label    string         `json:"label"`
	Value    float64        `json:"value"`
	Max      float64        `json:"max,omitempty"`
	Unit     string         `json:"unit,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	Tone     Tone           `json:"tone,omitempty"`
	Variant  string         `json:"variant,omitempty"`
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
	// Size is filled by the host after it validates the file.
	Size int64 `json:"size,omitempty"`
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
		if len(block.ColumnWidths) > 0 && len(block.ColumnWidths) != len(block.Columns) {
			return fmt.Errorf("result block %d: column widths must cover every column", i)
		}
		if len(block.RowIcons) > 0 && len(block.RowIcons) != len(block.Rows) {
			return fmt.Errorf("result block %d: row icons must cover every row", i)
		}
		if len(block.RowActions) > 0 {
			if block.Kind != BlockTable {
				return fmt.Errorf("result block %d: row actions are only supported on tables", i)
			}
			if len(block.RowActions) != len(block.Rows) {
				return fmt.Errorf("result block %d: row actions must cover every row", i)
			}
			for rowIndex, actions := range block.RowActions {
				for actionIndex, action := range actions {
					if strings.TrimSpace(action.Label) == "" {
						return fmt.Errorf("result block %d row %d action %d has no label", i, rowIndex, actionIndex)
					}
					switch action.Tone {
					case "", ToneNeutral, ToneSuccess, ToneWarning, ToneDanger:
					default:
						return fmt.Errorf("result block %d row %d action %d has unknown tone %q", i, rowIndex, actionIndex, action.Tone)
					}
					switch action.Variant {
					case "", "switch":
						if action.Input == nil {
							return fmt.Errorf("result block %d row %d action %d has no input", i, rowIndex, actionIndex)
						}
					case "select":
						if len(action.Options) == 0 {
							return fmt.Errorf("result block %d row %d action %d has no options", i, rowIndex, actionIndex)
						}
						found := false
						for optionIndex, option := range action.Options {
							if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Value) == "" {
								return fmt.Errorf("result block %d row %d action %d option %d has no label or value", i, rowIndex, actionIndex, optionIndex)
							}
							if option.Input == nil {
								return fmt.Errorf("result block %d row %d action %d option %d has no input", i, rowIndex, actionIndex, optionIndex)
							}
							if option.Value == action.Value {
								found = true
							}
						}
						if !found {
							return fmt.Errorf("result block %d row %d action %d value is not one of its options", i, rowIndex, actionIndex)
						}
					default:
						return fmt.Errorf("result block %d row %d action %d has unknown variant %q", i, rowIndex, actionIndex, action.Variant)
					}
				}
			}
		}
		if block.BatchAction != nil {
			if block.Kind != BlockTable {
				return fmt.Errorf("result block %d: batch actions are only supported on tables", i)
			}
			if len(block.RowActions) == 0 || len(block.RowActions) != len(block.Rows) {
				return fmt.Errorf("result block %d: batch actions require one row action entry per row", i)
			}
			if strings.TrimSpace(block.BatchAction.Label) == "" {
				return fmt.Errorf("result block %d: batch action has no label", i)
			}
			if block.BatchAction.Input == nil {
				return fmt.Errorf("result block %d: batch action has no input", i)
			}
			switch block.BatchAction.Tone {
			case "", ToneNeutral, ToneSuccess, ToneWarning, ToneDanger:
			default:
				return fmt.Errorf("result block %d: batch action has unknown tone %q", i, block.BatchAction.Tone)
			}
			for rowIndex, actions := range block.RowActions {
				if len(actions) == 0 {
					return fmt.Errorf("result block %d row %d: batch action row has no input", i, rowIndex)
				}
			}
		}
		switch block.Tone {
		case "", ToneNeutral, ToneSuccess, ToneWarning, ToneDanger:
		default:
			return fmt.Errorf("result block %d has unknown tone %q", i, block.Tone)
		}
	}
	return nil
}
