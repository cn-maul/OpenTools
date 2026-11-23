package tool

import (
	"fmt"
	"strconv"
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
}

// Block is one piece of rendered output. Data carries the structured payload
// for json, status-grid, meter and facts blocks.
//
// Tone is only valid on a notice block. StatusCell, StatusLegendItem,
// MeterItem and MeterSegment carry their own Tone for partial colouring.
type Block struct {
	Kind    BlockKind `json:"kind"`
	Title   string    `json:"title,omitempty"`
	Tone    Tone      `json:"tone,omitempty"`
	Text    string    `json:"text,omitempty"`
	Data    any       `json:"data,omitempty"`
	Columns []string  `json:"columns,omitempty"`
	// ColumnWidths assigns a width in DIP (device-independent pixels) to
	// each data column. The renderer appends its own width for the
	// row-action column. An empty entry lets the table size the column.
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
	Label   string `json:"label"`
	Confirm string `json:"confirm,omitempty"`
	// Input is used by a plain button. A choice menu uses the selected
	// option's Input instead, so this may be nil when Options is set.
	Input map[string]any `json:"input,omitempty"`
	// Options turns the action into a compact choice menu. Value is the
	// currently selected option and must be one of Options.
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

// MeterItem is one progress bar. Value is in the unit named by Unit,
// normally 0-100 for percentages.
type MeterItem struct {
	Label    string         `json:"label"`
	Value    float64        `json:"value"`
	Max      float64        `json:"max,omitempty"`
	Unit     string         `json:"unit,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	Tone     Tone           `json:"tone,omitempty"`
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
// Empty Tone renders in the muted idle color.
type StatusCell struct {
	Label  string `json:"label"`
	Tone   Tone   `json:"tone,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// StatusLegendItem maps one tone to a human-readable grid legend label.
type StatusLegendItem struct {
	Label string `json:"label"`
	Tone  Tone   `json:"tone,omitempty"`
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
	}
	return nil
}

func validateBlocks(blocks []Block) error {
	for i, block := range blocks {
		switch block.Kind {
		case BlockText, BlockNotice, BlockCode:
			if strings.TrimSpace(block.Text) == "" {
				return fmt.Errorf("result block %d (%s) has no text", i, block.Kind)
			}
		case BlockJSON, BlockGrid, BlockMeter, BlockFacts:
			if block.Data == nil {
				return fmt.Errorf("result block %d (%s) has no data", i, block.Kind)
			}
		case BlockTable:
			if len(block.Columns) == 0 {
				return fmt.Errorf("result block %d: table has no columns", i)
			}
			for rowIndex, row := range block.Rows {
				if len(row) != len(block.Columns) {
					return fmt.Errorf(
						"result block %d row %d has %d cells, want %d",
						i, rowIndex, len(row), len(block.Columns),
					)
				}
			}
		default:
			return fmt.Errorf("result block %d has unknown kind %q", i, block.Kind)
		}
		if block.Tone != "" && block.Kind != BlockNotice {
			return fmt.Errorf("result block %d: tone is only supported on notice blocks", i)
		}
		if len(block.ColumnWidths) > 0 && len(block.ColumnWidths) != len(block.Columns) {
			return fmt.Errorf("result block %d: column widths must cover every column", i)
		}
		for columnIndex, width := range block.ColumnWidths {
			if !validColumnWidth(width) {
				return fmt.Errorf(
					"result block %d column %d: width %q must be a non-negative DIP value such as \"120\" or \"120px\"",
					i, columnIndex, width,
				)
			}
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
					if len(action.Options) == 0 {
						if action.Input == nil {
							return fmt.Errorf("result block %d row %d action %d has no input", i, rowIndex, actionIndex)
						}
						continue
					}
					if len(action.Options) > 0 {
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
						if action.Value != "" && !found {
							return fmt.Errorf("result block %d row %d action %d value is not one of its options", i, rowIndex, actionIndex)
						}
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
			itemKey := block.BatchAction.ItemKey
			if itemKey == "" {
				itemKey = "item"
			}
			for rowIndex, actions := range block.RowActions {
				if len(actions) == 0 {
					return fmt.Errorf("result block %d row %d: batch action row has no input", i, rowIndex)
				}
				if _, ok := actions[0].Input[itemKey]; !ok {
					return fmt.Errorf(
						"result block %d row %d: batch action item key %q is missing from the row action input",
						i, rowIndex, itemKey,
					)
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

// validColumnWidth accepts the only width syntax the native table renderer
// implements: a non-negative DIP number with an optional "px" suffix. CSS units
// such as "%", "auto" and "em" are rejected so a tool cannot ship a layout
// that silently collapses to zero width.
func validColumnWidth(value string) bool {
	text := strings.TrimSpace(value)
	if text == "" {
		return true
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(text), "px"))
	number, err := strconv.ParseFloat(text, 64)
	return err == nil && number >= 0
}
