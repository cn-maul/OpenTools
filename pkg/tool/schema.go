package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Schema is the supported, deliberately small subset of JSON Schema used to
// render forms and validate inputs. UI carries rendering hints only.
type Schema struct {
	Type                 string             `json:"type,omitempty"`
	Title                string             `json:"title,omitempty"`
	Description          string             `json:"description,omitempty"`
	Default              any                `json:"default,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	Format               string             `json:"format,omitempty"`
	MinLength            *int               `json:"minLength,omitempty"`
	MaxLength            *int               `json:"maxLength,omitempty"`
	Pattern              string             `json:"pattern,omitempty"`
	Minimum              *float64           `json:"minimum,omitempty"`
	Maximum              *float64           `json:"maximum,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties bool               `json:"additionalProperties,omitempty"`
	UI                   *UIHints           `json:"x-ui,omitempty"`
}

// UIHints describe presentation, never validation.
type UIHints struct {
	Widget      string            `json:"widget,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	Group       string            `json:"group,omitempty"`
	Rows        int               `json:"rows,omitempty"`
	Secret      bool              `json:"secret,omitempty"`
	Accept      []string          `json:"accept,omitempty"`
	EnumLabels  map[string]string `json:"enumLabels,omitempty"`
	VisibleWhen *VisibleWhen      `json:"visibleWhen,omitempty"`
}

// VisibleWhen controls whether a field is shown for the current input. It is
// presentation-only; hidden fields keep their stored/default value.
type VisibleWhen struct {
	Field  string   `json:"field"`
	Equals any      `json:"equals,omitempty"`
	In     []string `json:"in,omitempty"`
}

// Object returns an object schema.
func Object(properties map[string]*Schema, required ...string) Schema {
	return Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	}
}

// String returns a string schema.
func String(title, description string) *Schema {
	return &Schema{Type: "string", Title: title, Description: description}
}

// Text returns a multi-line string schema.
func Text(title, description string, rows int) *Schema {
	return &Schema{
		Type:        "string",
		Title:       title,
		Description: description,
		UI:          &UIHints{Widget: "textarea", Rows: rows},
	}
}

// Number returns a numeric schema.
func Number(title, description string) *Schema {
	return &Schema{Type: "number", Title: title, Description: description}
}

// Boolean returns a boolean schema.
func Boolean(title, description string) *Schema {
	return &Schema{Type: "boolean", Title: title, Description: description}
}

// Select returns a string schema with fixed choices.
func Select(title, description string, values ...string) *Schema {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	return &Schema{Type: "string", Title: title, Description: description, Enum: enum}
}

// WithDefault returns a copy of s with a default value.
func (s *Schema) WithDefault(value any) *Schema {
	out := *s
	out.Default = value
	return &out
}

// WithMin sets a string length or numeric minimum.
func (s *Schema) WithMin(value float64) *Schema {
	out := *s
	if out.Type == "string" {
		n := int(value)
		out.MinLength = &n
	} else {
		out.Minimum = &value
	}
	return &out
}

// WithMax sets a string length or numeric maximum.
func (s *Schema) WithMax(value float64) *Schema {
	out := *s
	if out.Type == "string" {
		n := int(value)
		out.MaxLength = &n
	} else {
		out.Maximum = &value
	}
	return &out
}

// WithPattern sets a regular-expression constraint.
func (s *Schema) WithPattern(pattern string) *Schema {
	out := *s
	out.Pattern = pattern
	return &out
}

// WithWidget sets a UI widget hint.
func (s *Schema) WithWidget(widget, placeholder string) *Schema {
	out := *s
	ui := UIHints{}
	if out.UI != nil {
		ui = *out.UI
	}
	ui.Widget = widget
	ui.Placeholder = placeholder
	out.UI = &ui
	return &out
}

// WithGroup groups a field in the form.
func (s *Schema) WithGroup(group string) *Schema {
	out := *s
	ui := UIHints{}
	if out.UI != nil {
		ui = *out.UI
	}
	ui.Group = group
	out.UI = &ui
	return &out
}

// WithAccept limits a file picker to the listed extensions.
func (s *Schema) WithAccept(extensions ...string) *Schema {
	out := *s
	ui := UIHints{}
	if out.UI != nil {
		ui = *out.UI
	}
	ui.Accept = append([]string(nil), extensions...)
	out.UI = &ui
	return &out
}

// WithEnumLabels provides display labels while keeping the serialized enum
// values stable for validation, CLI calls, and stored job inputs.
func (s *Schema) WithEnumLabels(labels map[string]string) *Schema {
	out := *s
	ui := UIHints{}
	if out.UI != nil {
		ui = *out.UI
	}
	ui.EnumLabels = make(map[string]string, len(labels))
	for value, label := range labels {
		ui.EnumLabels[value] = label
	}
	out.UI = &ui
	return &out
}

// WithVisibleWhen shows the field only when the named sibling field matches
// one of the supplied values.
func (s *Schema) WithVisibleWhen(field string, values ...string) *Schema {
	out := *s
	ui := UIHints{}
	if out.UI != nil {
		ui = *out.UI
	}
	ui.VisibleWhen = &VisibleWhen{
		Field: field,
		In:    append([]string(nil), values...),
	}
	out.UI = &ui
	return &out
}

// Prepare applies defaults and validates input against s.
//
// It returns a detached copy so callers can persist the exact, normalized
// input of a job without mutating their request.
func Prepare(s Schema, values map[string]any) (map[string]any, error) {
	if values == nil {
		values = map[string]any{}
	}
	copied, err := cloneJSONMap(values)
	if err != nil {
		return nil, err
	}
	if err := applyDefaults(&s, copied, "$"); err != nil {
		return nil, err
	}
	if err := validate(s, copied, "$"); err != nil {
		return nil, err
	}
	return copied, nil
}

func cloneJSONMap(values map[string]any) (map[string]any, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode input: %w", err)
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func applyDefaults(s *Schema, value any, path string) error {
	switch s.Type {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		for name, child := range s.Properties {
			current, exists := obj[name]
			if !exists && child.Default != nil {
				obj[name] = cloneDefault(child.Default)
				current = obj[name]
				exists = true
			}
			if exists {
				if err := applyDefaults(child, current, path+"."+name); err != nil {
					return err
				}
			}
		}
	case "array":
		list, ok := value.([]any)
		if !ok || s.Items == nil {
			return nil
		}
		for i, item := range list {
			if err := applyDefaults(s.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneDefault(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return value
	}
	return out
}

func validate(s Schema, value any, path string) error {
	if value == nil {
		return nil
	}
	switch s.Type {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fieldError(path, MsgTypeObject, nil)
		}
		for _, name := range s.Required {
			item, exists := obj[name]
			if !exists || item == nil || (isBlankString(item)) {
				return fieldError(path+"."+name, MsgRequired, nil)
			}
		}
		for name, item := range obj {
			child, exists := s.Properties[name]
			if !exists {
				if !s.AdditionalProperties {
					return fieldError(path+"."+name, MsgUnknownField, nil)
				}
				continue
			}
			if err := validate(*child, item, path+"."+name); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fieldError(path, MsgTypeString, nil)
		}
		if s.MinLength != nil && len([]rune(text)) < *s.MinLength {
			return fieldError(path, MsgMinLength, map[string]string{"limit": fmt.Sprint(*s.MinLength)})
		}
		if s.MaxLength != nil && len([]rune(text)) > *s.MaxLength {
			return fieldError(path, MsgMaxLength, map[string]string{"limit": fmt.Sprint(*s.MaxLength)})
		}
		if s.Pattern != "" {
			re, err := regexp.Compile(s.Pattern)
			if err != nil {
				return fieldError(path, MsgInvalidPattern, nil)
			}
			if !re.MatchString(text) {
				return fieldError(path, MsgPattern, nil)
			}
		}
		if len(s.Enum) > 0 {
			allowed := make([]string, 0, len(s.Enum))
			for _, item := range s.Enum {
				allowed = append(allowed, fmt.Sprint(item))
			}
			if !slices.Contains(allowed, text) {
				return fieldError(path, MsgEnum, map[string]string{"values": strings.Join(allowed, ", ")})
			}
		}
	case "number", "integer":
		number, ok := numberValue(value)
		if !ok {
			return fieldError(path, MsgTypeNumber, nil)
		}
		if s.Type == "integer" && math.Trunc(number) != number {
			return fieldError(path, MsgTypeInteger, nil)
		}
		if s.Minimum != nil && number < *s.Minimum {
			return fieldError(path, MsgMinimum, map[string]string{"limit": fmt.Sprint(*s.Minimum)})
		}
		if s.Maximum != nil && number > *s.Maximum {
			return fieldError(path, MsgMaximum, map[string]string{"limit": fmt.Sprint(*s.Maximum)})
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fieldError(path, MsgTypeBoolean, nil)
		}
	case "array":
		list, ok := value.([]any)
		if !ok {
			return fieldError(path, MsgTypeArray, nil)
		}
		if s.Items != nil {
			for i, item := range list {
				if err := validate(*s.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func isBlankString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func numberValue(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		rv := reflect.ValueOf(value)
		if rv.IsValid() && rv.Kind() >= reflect.Int && rv.Kind() <= reflect.Float64 {
			return rv.Convert(reflect.TypeFor[float64]()).Float(), true
		}
		return 0, false
	}
}
