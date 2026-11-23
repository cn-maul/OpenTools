// Package pluginproto defines the JSON-RPC 2.0 process protocol shared by the
// OpenTools host and downloadable tool executables.
package pluginproto

import (
	"encoding/json"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

const (
	// Version is the host/tool process protocol version.
	Version = 2
	// MinimumVersion is the oldest installed plugin protocol the host keeps
	// loading. Version 1 predates streamed result sections.
	MinimumVersion = 1

	// ContractVersion identifies the shape of the host/tool contract, which
	// is independent of the process protocol: a host may keep speaking
	// protocol 2 while adding fields, capabilities and host services. It only
	// grows when a field changes meaning or is removed.
	ContractVersion = 3
	// MinimumContractVersion is the oldest tool contract this host can run.
	// Contract 3 removed result fields and changed env.DataDir semantics, so a
	// tool built against an older contract must be rebuilt instead of failing
	// later with an unknown result block.
	MinimumContractVersion = 3

	JSONRPCVersion = "2.0"

	MethodInitialize = "initialize"
	MethodRun        = "tool/run"
	MethodLog        = "tool/log"
	MethodProgress   = "tool/progress"
	MethodSection    = "tool/section"
	// MethodCancel is a host notification asking a tool to stop. The tool
	// cancels the context passed to Run and reports its cancellation; the
	// host escalates to terminating the process if it does not comply.
	MethodCancel = "$/cancel"
)

// Host service methods a tool may call. Every one of them requires the
// matching capability to have been granted by initialize.
const (
	MethodHostDialogOpenFile = "host/dialog/openFile"
	MethodHostDialogSaveFile = "host/dialog/saveFile"
	MethodHostDialogPickDir  = "host/dialog/pickDirectory"
	MethodHostSecretsGet     = "host/secrets/get"
	MethodHostSecretsSet     = "host/secrets/set"
	MethodHostSecretsDelete  = "host/secrets/delete"
	MethodHostConfigGet      = "host/config/get"
	MethodHostConfigSet      = "host/config/set"
	MethodHostNotify         = "host/notify"
	MethodHostElevate        = "host/elevate"
)

// Capability names. The constants alias the contract package so a tool and
// the host cannot drift on spelling.
const (
	CapabilitySecrets = tool.CapabilitySecrets
	CapabilityDialogs = tool.CapabilityDialogs
	CapabilityConfig  = tool.CapabilityConfig
	CapabilityNotify  = tool.CapabilityNotify
	CapabilityElevate = tool.CapabilityElevate
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
	// CodeCapabilityDenied rejects a host service call whose capability was
	// not granted in Definition.Requires.
	CodeCapabilityDenied = -32001
)

// InitializeParams is sent once before a tool invocation.
type InitializeParams struct {
	ProtocolVersion int    `json:"protocolVersion"`
	ToolID          string `json:"toolId"`
	WorkDir         string `json:"workDir"`
	// DataDir is the host's private durable directory for this tool. Older
	// hosts sent the shared OpenTools data root here.
	DataDir string `json:"dataDir"`
	// ContractVersion is the host's contract; zero means the host predates
	// it. A tool may use it to avoid calling a service it cannot rely on.
	ContractVersion int `json:"contractVersion,omitempty"`
	// GrantedCapabilities is the subset of Definition.Requires the host
	// accepts. Absent means "none granted", never "all granted".
	GrantedCapabilities []tool.Capability `json:"grantedCapabilities,omitempty"`
	// Limits are the host budgets echoed back in InitializeResult.
	Limits Limits `json:"limits,omitempty"`
}

// InitializeResult confirms that the executable speaks the requested
// protocol and is the expected tool.
type InitializeResult struct {
	ProtocolVersion int `json:"protocolVersion"`
	// ContractVersion is the contract the host applies to this session.
	ContractVersion int    `json:"contractVersion,omitempty"`
	ToolID          string `json:"toolId"`
	Version         string `json:"version"`
	// Capabilities is the granted subset of Definition.Requires. A tool must
	// not call a host service whose capability is missing; the host rejects
	// it with CodeCapabilityDenied.
	Capabilities []tool.Capability `json:"capabilities,omitempty"`
	// Limits tells the tool what the host enforces, so a tool can split a
	// large result instead of having it rejected.
	Limits Limits `json:"limits,omitempty"`
}

// Limits are the host budgets for one session.
type Limits struct {
	// MaxMessageBytes caps one encoded JSON-RPC message in either direction.
	MaxMessageBytes int64 `json:"maxMessageBytes,omitempty"`
	// MaxJobDurationMS is the wall-clock budget of one invocation, zero when
	// the host does not enforce one.
	MaxJobDurationMS int64 `json:"maxJobDurationMs,omitempty"`
	// CancelGraceMS is how long a tool has between "$/cancel" and having its
	// process terminated.
	CancelGraceMS int64 `json:"cancelGraceMs,omitempty"`
}

// CancelParams is the payload of a "$/cancel" notification.
type CancelParams struct {
	Reason string `json:"reason,omitempty"`
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
