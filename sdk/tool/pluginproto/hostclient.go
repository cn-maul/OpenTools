package pluginproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

// HostClient is the tool-side entry point for host services. The host
// executable provides the implementation; a nil client means the tool runs
// without a host.
type HostClient interface {
	Call(ctx context.Context, method string, params, result any) error
}

// hostBridge adapts a Conn to tool.Host and enforces capability grants.
type hostBridge struct {
	conn     *Conn
	services HostClient

	mu      sync.RWMutex
	granted map[tool.Capability]struct{}
}

func newHostClient(conn *Conn, services HostClient) *hostBridge {
	return &hostBridge{conn: conn, services: services, granted: map[tool.Capability]struct{}{}}
}

func (b *hostBridge) setGranted(names []tool.Capability) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.granted = make(map[tool.Capability]struct{}, len(names))
	for _, name := range names {
		b.granted[name] = struct{}{}
	}
}

func (b *hostBridge) Granted(capability tool.Capability) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.granted[capability]
	return ok
}

// Call implements tool.Host. When a host executable was supplied it takes
// precedence; otherwise the request travels back over the stdio connection.
func (b *hostBridge) Call(ctx context.Context, method string, params, result any) error {
	if b.services != nil {
		return b.services.Call(ctx, method, params, result)
	}
	if b.conn == nil {
		return fmt.Errorf("%s: no host is connected", method)
	}
	return b.conn.Request(ctx, method, params, result)
}

// CapabilityFor maps a host method to the capability it requires.
func CapabilityFor(method string) tool.Capability {
	switch method {
	case MethodHostSecretsGet, MethodHostSecretsSet, MethodHostSecretsDelete:
		return CapabilitySecrets
	case MethodHostDialogOpenFile, MethodHostDialogSaveFile, MethodHostDialogPickDir:
		return CapabilityDialogs
	case MethodHostConfigGet, MethodHostConfigSet:
		return CapabilityConfig
	case MethodHostNotify:
		return CapabilityNotify
	case MethodHostElevate:
		return CapabilityElevate
	default:
		return ""
	}
}

// CheckGranted returns a capability error when the tool calls a service it
// did not declare.
func CheckGranted(required []tool.Capability, method string) error {
	capability := CapabilityFor(method)
	if capability == "" {
		return errors.New("unknown host method " + method)
	}
	for _, name := range required {
		if name == capability {
			return nil
		}
	}
	return &RemoteError{
		Code:    CodeCapabilityDenied,
		Message: fmt.Sprintf("tool did not declare the %q capability", capability),
	}
}

// HostError decodes a RemoteError from a host call, if any.
func HostError(err error) (*RemoteError, bool) {
	var remote *RemoteError
	if errors.As(err, &remote) {
		return remote, true
	}
	return nil, false
}

// DecodeParams decodes raw JSON-RPC params into dst, tolerating null.
func DecodeParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dst)
}
