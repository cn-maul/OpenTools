package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

// form renders the tool's input schema. The model is the Shell's values map,
// which keeps typed values across frames while the widgets are rebuilt.
func (s *Shell) form(c *ui.Context, def tool.Definition) {
	if len(def.Input.Properties) == 0 {
		return
	}
	t := s.theme
	columns := 2
	if width, _ := c.Size(); width < 860 {
		columns = 1
	}
	ui.Column(c).FillWidth().Padding(14).Gap(12).Radius(10).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "输入").FontSize(11).Bold().TextColor(t.TextMuted).Grow(1)
			ui.Text(c, "*").FontSize(10.5).Bold().TextColor(t.Danger)
			ui.Text(c, "必填").FontSize(10.5).TextColor(t.TextMuted)
		})
		ui.Grid(c).Columns(columns).GapX(14).GapY(10).Children(func() {
			s.formFields(c, def.Input, s.values, "", columns, 0)
		})
	})
}

func (s *Shell) formFields(c *ui.Context, schema tool.Schema, model map[string]any, prefix string, columns, depth int) {
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	// Map iteration is random; this order is stable enough for a deterministic
	// layout without changing the schema contract.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}
	for _, name := range names {
		child := schema.Properties[name]
		if child == nil || !tool.FieldVisible(*child, model) {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		value, exists := model[name]
		if !exists && child.Default != nil {
			model[name] = cloneJSON(child.Default)
			value = model[name]
		}
		if child.Type == "object" {
			s.objectField(c, *child, model, name, path, required[name], columns, depth)
			continue
		}
		if child.UI != nil && child.UI.Widget == "hidden" {
			continue
		}
		wide := child.Type == "array" || child.UI != nil &&
			(child.UI.Widget == "code" || child.UI.Widget == "textarea" || child.UI.Rows > 0 || child.UI.Widget == "file")
		field := ui.Column(c).Gap(4).MinWidth(0)
		if wide {
			field.ColumnSpan(columns)
		}
		field.Children(func() {
			s.field(c, *child, model, name, path, value, required[name], depth)
		})
	}
}

func (s *Shell) objectField(c *ui.Context, schema tool.Schema, model map[string]any, name, path string, required bool, columns, depth int) {
	nested, ok := model[name].(map[string]any)
	if !ok {
		nested = schemaDefaults(schema)
		model[name] = nested
	}
	title := schema.Title
	if title == "" {
		title = name
	}
	if required {
		title += "（必填）"
	}
	ui.Fieldset(c, title, func() {
		ui.Grid(c).Columns(columns).GapX(14).GapY(10).Children(func() {
			s.formFields(c, schema, nested, path, columns, depth+1)
		})
	}).ColumnSpan(columns).FillWidth()
}

func (s *Shell) field(c *ui.Context, schema tool.Schema, model map[string]any, name, path string, value any, required bool, depth int) {
	t := s.theme
	label := schema.Title
	if label == "" {
		label = name
	}
	ui.Row(c).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(11.5).Bold()
		if required {
			ui.Text(c, "必填").FontSize(10).TextColor(t.Danger)
		}
	})
	hints := schema.UI
	if schema.Description != "" {
		ui.Text(c, schema.Description).FontSize(10.5).TextColor(t.TextMuted).Wrap()
	}

	changed := func(next any) {
		model[name] = next
		s.errText = ""
		s.fieldErr = nil
	}

	switch {
	case schema.Type == "boolean":
		on, _ := value.(bool)
		if ui.Switch(c.Key("switch:"+path), &on).Label(label).Changed() {
			changed(on)
		}
	case len(schema.Enum) > 0:
		s.enumField(c, schema, value, path, label, changed)
	case hints != nil && hints.Widget == "directory":
		s.pathField(c, schema, value, path, label, true, changed)
	case hints != nil && hints.Widget == "file":
		s.pathField(c, schema, value, path, label, false, changed)
	case hints != nil && hints.Widget == "code":
		s.codeField(c, schema, value, path, label, changed)
	case schema.Type == "array":
		s.arrayField(c, schema, value, path, label, changed)
	case schema.Type == "number" || schema.Type == "integer":
		s.numberField(c, schema, value, path, changed)
	case hints != nil && (hints.Widget == "textarea" || hints.Rows > 0):
		text, _ := value.(string)
		rows := hints.Rows
		if rows <= 0 {
			rows = 5
		}
		area := ui.TextArea(c.Key("area:"+path), &text).Placeholder(hints.Placeholder).
			Label(label).Lines(rows, max(rows+8, rows))
		if area.Changed() {
			changed(text)
		}
	default:
		text, _ := value.(string)
		input := ui.TextInput(c.Key("text:"+path), &text).Label(label)
		if hints != nil {
			input.Placeholder(hints.Placeholder)
			if hints.Secret {
				input.Password()
			}
		}
		if schema.MaxLength != nil {
			input.MaxLength(*schema.MaxLength)
		}
		if input.Changed() {
			changed(text)
		}
	}

	if s.fieldErr != nil && s.fieldErr.Field == path {
		ui.Text(c, s.fieldErr.Message).FontSize(10.5).TextColor(t.Danger).Wrap()
	}
}

func (s *Shell) enumField(c *ui.Context, schema tool.Schema, value any, path, label string, changed func(any)) {
	t := s.theme
	current, _ := value.(string)
	sel := ui.SelectBase(c.Key("enum:"+path), &current)
	sel.Trigger.FillWidth().Height(30).Padding(0, 9).Gap(6).Radius(7).
		Background(t.Surface).Border(1, t.Border).Label(label).Children(func() {
		text := current
		if schema.UI != nil && schema.UI.EnumLabels != nil {
			if display, ok := schema.UI.EnumLabels[current]; ok {
				text = display
			}
		}
		ui.Text(c, text).FontSize(12).Grow(1).SingleLine()
		ui.Text(c, "⌄").FontSize(11).TextColor(t.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Radius(8).Background(t.Background).Border(1, t.Border).MinWidth(180)
		for _, option := range schema.Enum {
			key := fmt.Sprint(option)
			item := sel.Item(key).Padding(5, 9).Radius(5)
			if item.Highlighted() {
				item.Background(t.Accent).TextColor(t.AccentText)
			}
			item.Children(func() {
				text := key
				if schema.UI != nil && schema.UI.EnumLabels != nil {
					if display, ok := schema.UI.EnumLabels[key]; ok {
						text = display
					}
				}
				ui.Text(c, text).FontSize(12)
			})
		}
	})
	if sel.Trigger.Changed() {
		changed(current)
	}
}

func (s *Shell) numberField(c *ui.Context, schema tool.Schema, value any, path string, changed func(any)) {
	number := numberValue(value)
	lo, hi := -1e15, 1e15
	if schema.Minimum != nil {
		lo = *schema.Minimum
	}
	if schema.Maximum != nil {
		hi = *schema.Maximum
	}
	step := 1.0
	if schema.Type == "number" {
		step = 0
	}
	input := ui.NumberInput(c.Key("number:"+path), &number, lo, hi, step)
	if input.Changed() {
		if schema.Type == "integer" {
			changed(int64(number))
		} else {
			changed(number)
		}
	}
}

func (s *Shell) pathField(c *ui.Context, schema tool.Schema, value any, path, label string, directory bool, changed func(any)) {
	t := s.theme
	text, _ := value.(string)
	placeholder := "选择一个文件"
	if directory {
		placeholder = "选择一个文件夹"
	}
	if schema.UI != nil && schema.UI.Placeholder != "" {
		placeholder = schema.UI.Placeholder
	}
	ui.Row(c).Gap(6).Children(func() {
		input := ui.TextInput(c.Key("path:"+path), &text).Grow(1).FontSize(11.5).
			Placeholder(placeholder).Label(label)
		if input.Changed() {
			changed(text)
		}
		if ui.Button(c, "选择").Clicked() {
			if directory {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
					Parent: s.win, Title: "选择文件夹", Directory: true, ButtonLabel: "选择",
				})
				if err == nil && len(paths) > 0 {
					changed(paths[0])
				}
			} else {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
					Parent: s.win, Title: "选择文件", ButtonLabel: "选择",
					Filters: pathFilters(schema.UI),
				})
				if err == nil && len(paths) > 0 {
					changed(paths[0])
				}
			}
		}
	})
	_ = t
}

func (s *Shell) codeField(c *ui.Context, schema tool.Schema, value any, path, label string, changed func(any)) {
	text, _ := value.(string)
	rows := 12
	if schema.UI != nil && schema.UI.Rows > 0 {
		rows = schema.UI.Rows
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Children(func() {
			ui.Text(c, "JSON 源码").FontSize(10.5).TextColor(s.theme.TextMuted).Grow(1)
			if ui.Button(c, "从文件载入").Clicked() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
					Parent: s.win, Title: "选择文件", ButtonLabel: "载入",
					Filters: pathFilters(schema.UI),
				})
				if err == nil && len(paths) > 0 {
					if data, readErr := os.ReadFile(paths[0]); readErr == nil {
						changed(string(data))
					} else {
						s.toast(readErr.Error())
					}
				}
			}
		})
		area := ui.TextArea(c.Key("code:"+path), &text).FontSize(12).Lines(rows, rows+10).Label(label)
		if schema.UI != nil {
			area.Placeholder(schema.UI.Placeholder)
		}
		if area.Changed() {
			changed(text)
		}
	})
}

func (s *Shell) arrayField(c *ui.Context, schema tool.Schema, value any, path, label string, changed func(any)) {
	t := s.theme
	draft, ok := s.arrayDrafts[path]
	if !ok {
		data, _ := json.MarshalIndent(value, "", "  ")
		draft = string(data)
		s.arrayDrafts[path] = draft
	}
	input := ui.TextInput(c.Key("array:"+path), &draft).FontSize(11.5).Label(label).
		Placeholder("[]")
	if schema.UI != nil {
		input.Placeholder(schema.UI.Placeholder)
	}
	if input.Changed() {
		s.arrayDrafts[path] = draft
		var parsed any
		if err := json.Unmarshal([]byte(draft), &parsed); err != nil {
			s.arrayErrors[path] = "请输入有效的 JSON 数组。"
		} else if _, ok := parsed.([]any); !ok {
			s.arrayErrors[path] = "请输入有效的 JSON 数组。"
		} else {
			delete(s.arrayErrors, path)
			changed(parsed)
		}
	}
	if message := s.arrayErrors[path]; message != "" {
		ui.Text(c, message).FontSize(10.5).TextColor(t.Danger)
	}
}

func numberValue(value any) float64 {
	switch n := value.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func pathFilters(hints *tool.UIHints) []mygo.FileFilter {
	if hints == nil || len(hints.Accept) == 0 {
		return nil
	}
	extensions := make([]string, 0, len(hints.Accept))
	for _, extension := range hints.Accept {
		extension = strings.TrimPrefix(strings.TrimSpace(extension), ".")
		if extension != "" {
			extensions = append(extensions, extension)
		}
	}
	if len(extensions) == 0 {
		return nil
	}
	return []mygo.FileFilter{{
		Name:       strings.ToUpper(strings.Join(extensions, ", ")) + " 文件",
		Extensions: extensions,
	}}
}
