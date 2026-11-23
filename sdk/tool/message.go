package tool

import (
	"fmt"
	"strings"
)

// Message codes belong to the tool contract, not to a transport or a UI.
const (
	MsgRequired       = "required"
	MsgUnknownField   = "unknown_field"
	MsgTypeObject     = "type_object"
	MsgTypeString     = "type_string"
	MsgTypeNumber     = "type_number"
	MsgTypeInteger    = "type_integer"
	MsgTypeBoolean    = "type_boolean"
	MsgTypeArray      = "type_array"
	MsgMinLength      = "min_length"
	MsgMaxLength      = "max_length"
	MsgPattern        = "pattern"
	MsgEnum           = "enum"
	MsgMinimum        = "minimum"
	MsgMaximum        = "maximum"
	MsgInvalidPattern = "invalid_pattern"
)

// FieldError is a validation failure that a form can attach to one field.
// The unexported Rust-style encoding keeps the generic error interface while
// allowing adapters to preserve the field and message code.
type FieldError struct {
	Code    string
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	return e.Code + "|" + e.Field + "|" + e.Message
}

// Message renders the default display text for a contract message code.
func Message(code string, params map[string]string) string {
	switch code {
	case MsgRequired:
		return "此项为必填项。"
	case MsgUnknownField:
		return "无法识别此字段。"
	case MsgTypeObject:
		return "请输入对象。"
	case MsgTypeString:
		return "请输入文本。"
	case MsgTypeNumber:
		return "请输入数字。"
	case MsgTypeInteger:
		return "请输入整数。"
	case MsgTypeBoolean:
		return "请选择开启或关闭。"
	case MsgTypeArray:
		return "请输入列表。"
	case MsgMinLength:
		return fmt.Sprintf("至少输入 %s 个字符。", params["limit"])
	case MsgMaxLength:
		return fmt.Sprintf("最多输入 %s 个字符。", params["limit"])
	case MsgPattern:
		return "内容格式不正确。"
	case MsgEnum:
		return fmt.Sprintf("请选择其中一个值：%s。", params["values"])
	case MsgMinimum:
		return fmt.Sprintf("请输入不小于 %s 的值。", params["limit"])
	case MsgMaximum:
		return fmt.Sprintf("请输入不大于 %s 的值。", params["limit"])
	case MsgInvalidPattern:
		return "字段的 Schema 校验规则无效。"
	default:
		return code
	}
}

func fieldError(path, code string, params map[string]string) error {
	field := strings.TrimPrefix(path, "$.")
	if field == "$" || field == "" {
		field = "input"
	}
	return &FieldError{Code: code, Field: field, Message: Message(code, params)}
}
