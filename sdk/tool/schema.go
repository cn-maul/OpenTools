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
	Rows        int               `json:"rows,omitempty"`
	Secret      bool              `json:"secret,omitempty"`
	Accept      []string          `json:"accept,omitempty"`
	EnumLabels  map[string]string `json:"enumLabels,omitempty"`
	VisibleWhen *VisibleWhen      `json:"visibleWhen,omitempty"`
}

// VisibleWhen controls whether a field is shown for the current input. It is
// presentation-only; hidden fields keep their stored/default value.
type VisibleWhen struct {
	// Field names a sibling property; In lists the values that show this
	// field. An empty In means "always visible".
	Field string   `json:"field"`
	In    []string `json:"in,omitempty"`
}

// FieldVisible reports whether a field should be shown for the current
// sibling values. The host uses it to render forms, and validation uses it so
// a hidden field never blocks submission. Hidden values are still preserved
// and passed to the tool.
func FieldVisible(schema Schema, siblings map[string]any) bool {
	if schema.UI == nil || schema.UI.VisibleWhen == nil {
		return true
	}
	condition := schema.UI.VisibleWhen
	if len(condition.In) == 0 {
		return true
	}
	text := fmt.Sprint(siblings[condition.Field])
	for _, allowed := range condition.In {
		if text == allowed {
			return true
		}
	}
	return false
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

// WithSecret marks a string field so forms render it as a password input.
// The host also uses this flag to redact the value before a job snapshot is
// persisted (see Redact); the tool still receives the real value.
func (s *Schema) WithSecret() *Schema {
	out := *s
	ui := UIHints{}
	if out.UI != nil {
		ui = *out.UI
	}
	ui.Secret = true
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

// ValidateSchema checks the parts of a form schema that the host relies on
// before any user input exists. Runtime validation in Prepare covers values;
// this catches authoring mistakes that would otherwise surface only when
// someone fills the form or when an action submits hidden fields.
func ValidateSchema(schema Schema) error {
	if schema.Type == "" {
		schema.Type = "object"
	}
	return validateSchema(schema, "$")
}

func validateSchema(schema Schema, path string) error {
	switch schema.Type {
	case "object":
		for _, name := range schema.Required {
			if child, ok := schema.Properties[name]; !ok || child == nil {
				return fmt.Errorf("%s: required property %q is not defined", path, name)
			}
		}
		for name, child := range schema.Properties {
			if child == nil {
				return fmt.Errorf("%s: property %q has a nil schema", path, name)
			}
			if err := checkVisibleWhen(schema, path, name, *child); err != nil {
				return err
			}
			if err := validateSchema(*child, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		if schema.Items != nil {
			if err := validateSchema(*schema.Items, path+"[]"); err != nil {
				return err
			}
		}
	case "string", "number", "integer", "boolean":
	default:
		return fmt.Errorf("%s: unknown type %q", path, schema.Type)
	}
	if schema.Pattern != "" {
		if schema.Type != "string" {
			return fmt.Errorf("%s: pattern is only supported on string fields", path)
		}
		if _, err := regexp.Compile(schema.Pattern); err != nil {
			return fmt.Errorf("%s: invalid pattern: %w", path, err)
		}
	}
	if schema.MinLength != nil && *schema.MinLength < 0 {
		return fmt.Errorf("%s: min length cannot be negative", path)
	}
	if schema.MaxLength != nil && *schema.MaxLength < 0 {
		return fmt.Errorf("%s: max length cannot be negative", path)
	}
	if schema.MinLength != nil && schema.MaxLength != nil && *schema.MinLength > *schema.MaxLength {
		return fmt.Errorf("%s: min length exceeds max length", path)
	}
	if (schema.MinLength != nil || schema.MaxLength != nil) && schema.Type != "string" {
		return fmt.Errorf("%s: length limits are only supported on string fields", path)
	}
	if schema.Minimum != nil && schema.Maximum != nil && *schema.Minimum > *schema.Maximum {
		return fmt.Errorf("%s: minimum exceeds maximum", path)
	}
	if (schema.Minimum != nil || schema.Maximum != nil) &&
		schema.Type != "number" && schema.Type != "integer" {
		return fmt.Errorf("%s: minimum/maximum are only supported on number fields", path)
	}
	if len(schema.Enum) > 0 && schema.Type != "string" {
		return fmt.Errorf("%s: enum is only supported on string fields", path)
	}
	if schema.Default != nil {
		if err := validate(schema, cloneDefault(schema.Default), path); err != nil {
			return fmt.Errorf("%s: invalid default: %w", path, err)
		}
	}
	if schema.UI == nil {
		return nil
	}
	switch schema.UI.Widget {
	case "":
	case "hidden":
		if schema.Type == "object" {
			return fmt.Errorf("%s: hidden is not supported on object fields", path)
		}
	case "file", "directory", "code", "textarea":
		if schema.Type != "string" {
			return fmt.Errorf("%s: widget %q is only supported on string fields", path, schema.UI.Widget)
		}
	default:
		return fmt.Errorf("%s: unknown widget %q", path, schema.UI.Widget)
	}
	if condition := schema.UI.VisibleWhen; condition != nil {
		if strings.TrimSpace(condition.Field) == "" {
			return fmt.Errorf("%s: visibleWhen requires a field", path)
		}
	}
	for value := range schema.UI.EnumLabels {
		found := false
		for _, option := range schema.Enum {
			if fmt.Sprint(option) == value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: enum label %q has no matching enum value", path, value)
		}
	}
	if schema.UI.Rows < 0 {
		return fmt.Errorf("%s: rows cannot be negative", path)
	}
	if schema.UI.Rows > 0 && schema.Type != "string" {
		return fmt.Errorf("%s: rows is only supported on string fields", path)
	}
	if schema.UI.Secret && schema.Type != "string" {
		return fmt.Errorf("%s: secret is only supported on string fields", path)
	}
	if len(schema.UI.Accept) > 0 && schema.UI.Widget != "file" {
		return fmt.Errorf("%s: accept is only supported on the file widget", path)
	}
	return nil
}

// checkVisibleWhen verifies that a nested field's condition names a sibling.
// It lives apart from validateSchema because sibling lookup only makes sense
// once the whole object, not one property, is known.
func checkVisibleWhen(parent Schema, path string, childName string, child Schema) error {
	if child.UI == nil || child.UI.VisibleWhen == nil {
		return nil
	}
	field := strings.TrimSpace(child.UI.VisibleWhen.Field)
	if field == "" {
		return fmt.Errorf("%s.%s: visibleWhen requires a field", path, childName)
	}
	if _, ok := parent.Properties[field]; !ok {
		return fmt.Errorf("%s.%s: visibleWhen references unknown sibling %q", path, childName, field)
	}
	return nil
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

// RedactedPlaceholder replaces secret field values before they are stored.
const RedactedPlaceholder = "<redacted>"

// Redact returns a copy of values with every field marked secret in s
// replaced by RedactedPlaceholder. Callers use it on their way to durable
// storage so a password or API key never reaches a job snapshot.
//
// The returned map is meant for display only; the tool must still be given
// the original values.
func Redact(s Schema, values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	copied, err := cloneJSONMap(values)
	if err != nil {
		copied = make(map[string]any, len(values))
		for key, value := range values {
			copied[key] = value
		}
	}
	redactInto(&s, copied)
	return copied
}

func redactInto(s *Schema, value any) {
	if value == nil {
		return
	}
	switch s.Type {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return
		}
		for name, child := range s.Properties {
			if child == nil {
				continue
			}
			current, exists := obj[name]
			if !exists {
				continue
			}
			if child.UI != nil && child.UI.Secret {
				obj[name] = RedactedPlaceholder
				continue
			}
			redactInto(child, current)
		}
	case "array":
		list, ok := value.([]any)
		if !ok || s.Items == nil {
			return
		}
		for _, item := range list {
			redactInto(s.Items, item)
		}
	}
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
			if child, exists := s.Properties[name]; exists && child != nil && !FieldVisible(*child, obj) {
				continue
			}
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
			if child == nil || !FieldVisible(*child, obj) {
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
