// Package pluginproto defines the JSON-RPC 2.0 process protocol shared by the
// OpenTools host and downloadable tool executables.
package pluginproto

import (
	"encoding/json"

	"opentools/pkg/tool"
)

const (
	// Version is the host/tool process protocol version.
	Version = 2
	// MinimumVersion is the oldest installed plugin protocol the host keeps
	// loading. Version 1 predates streamed result sections.
	MinimumVersion = 1

	JSONRPCVersion = "2.0"

	MethodInitialize = "initialize"
	MethodRun        = "tool/run"
	MethodLog        = "tool/log"
	MethodProgress   = "tool/progress"
	MethodSection    = "tool/section"
)

// SupportsVersion reports whether the host can load and invoke a plugin that
// speaks the given process protocol version.
func SupportsVersion(version int) bool {
	return version >= MinimumVersion && version <= Version
}

// Request is one JSON-RPC request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is one JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is the JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeToolError      = -32000
	CodeCanceled       = -32800
)

// InitializeParams is sent once before a tool invocation.
type InitializeParams struct {
	ProtocolVersion int    `json:"protocolVersion"`
	ToolID          string `json:"toolId"`
	WorkDir         string `json:"workDir"`
	DataDir         string `json:"dataDir"`
}

// InitializeResult confirms that the executable speaks the requested
// protocol and is the expected tool.
type InitializeResult struct {
	ProtocolVersion int    `json:"protocolVersion"`
	ToolID          string `json:"toolId"`
	Version         string `json:"version"`
}

// RunParams contains the validated input for one tool invocation.
type RunParams struct {
	Input map[string]any `json:"input"`
}

// LogParams is the payload of a tool/log notification.
type LogParams struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ProgressParams is the payload of a tool/progress notification.
type ProgressParams struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// SectionParams is the payload of a tool/section notification.
type SectionParams struct {
	Section tool.Section `json:"section"`
}

// Manifest is stored inside every downloaded tool archive.
type Manifest struct {
	SchemaVersion   int             `json:"schemaVersion"`
	ProtocolVersion int             `json:"protocolVersion"`
	Platform        string          `json:"platform"`
	Executable      string          `json:"executable"`
	Author          string          `json:"author,omitempty"`
	Homepage        string          `json:"homepage,omitempty"`
	License         string          `json:"license,omitempty"`
	Definition      tool.Definition `json:"definition"`
}
