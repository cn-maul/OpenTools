// Package hostservice implements the host services a tool can call.
//
// It is the only place that knows what a "secrets" or "dialogs" request
// means. The plugin transport forwards a method name and parameters here, so
// adding a service does not change the process protocol.
package hostservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cn-maul/OpenTools/sdk/tool"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

// UI is the set of native interactions the host exposes. The desktop adapter
// implements it; a headless host can return ErrUnavailable.
type UI interface {
	OpenFile(ctx context.Context, title string, extensions []string) (string, error)
	SaveFile(ctx context.Context, title, suggested string, extensions []string) (string, error)
	PickDirectory(ctx context.Context, title string) (string, error)
	Notify(ctx context.Context, title, body string) error
	Elevate(ctx context.Context, params tool.ElevateRequest) (tool.ElevateResult, error)
}

// ErrUnavailable is returned by a UI implementation that cannot show the
// requested surface, such as a console-only build.
var ErrUnavailable = errors.New("this host cannot show native dialogs")

// Service dispatches tool-initiated calls.
type Service struct {
	dataDir string
	ui      UI
	logger  *slog.Logger

	// granted maps tool ID to the capabilities its definition declared.
	// The transport already enforces this; repeating it here keeps the
	// dispatcher safe if it is called directly.
	required func(toolID string) ([]tool.Capability, bool)
}

// Options configures a Service.
type Options struct {
	DataDir string
	UI      UI
	Logger  *slog.Logger
	// Required returns the capabilities a tool declared, and whether the
	// tool is installed.
	Required func(toolID string) ([]tool.Capability, bool)
}

// New returns a host service dispatcher.
func New(options Options) *Service {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		dataDir:  options.DataDir,
		ui:       options.UI,
		logger:   logger,
		required: options.Required,
	}
}

// Capabilities lists the services this host actually implements. A tool only
// receives the intersection of this list and its own Definition.Requires, so
// declaring a capability the host lacks fails at the call, not at install.
func (s *Service) Capabilities() []tool.Capability {
	names := []tool.Capability{
		pluginproto.CapabilitySecrets,
		pluginproto.CapabilityConfig,
	}
	if s.ui != nil {
		names = append(names,
			pluginproto.CapabilityDialogs,
			pluginproto.CapabilityNotify,
			pluginproto.CapabilityElevate,
		)
	}
	return names
}

// Handle implements tool.HostServices.
func (s *Service) Handle(ctx context.Context, toolID, method string, params json.RawMessage) (any, error) {
	if method == "" {
		return nil, &pluginproto.RemoteError{Code: pluginproto.CodeMethodNotFound, Message: "empty host method"}
	}
	capability := pluginproto.CapabilityFor(method)
	if capability == "" {
		return nil, &pluginproto.RemoteError{
			Code:    pluginproto.CodeMethodNotFound,
			Message: fmt.Sprintf("host method %q does not exist", method),
		}
	}
	if !s.allows(toolID, capability) {
		return nil, &pluginproto.RemoteError{
			Code:    pluginproto.CodeCapabilityDenied,
			Message: fmt.Sprintf("tool %q did not declare the %q capability", toolID, capability),
		}
	}

	switch method {
	case pluginproto.MethodHostDialogOpenFile:
		var request struct {
			Title      string   `json:"title"`
			Extensions []string `json:"extensions"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if s.ui == nil {
			return nil, &pluginproto.RemoteError{Code: pluginproto.CodeCapabilityDenied, Message: ErrUnavailable.Error()}
		}
		path, err := s.ui.OpenFile(ctx, request.Title, request.Extensions)
		if err != nil {
			return nil, wrap(err)
		}
		return map[string]any{"path": path}, nil

	case pluginproto.MethodHostDialogSaveFile:
		var request struct {
			Title      string   `json:"title"`
			Suggested  string   `json:"suggested"`
			Extensions []string `json:"extensions"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if s.ui == nil {
			return nil, &pluginproto.RemoteError{Code: pluginproto.CodeCapabilityDenied, Message: ErrUnavailable.Error()}
		}
		path, err := s.ui.SaveFile(ctx, request.Title, request.Suggested, request.Extensions)
		if err != nil {
			return nil, wrap(err)
		}
		return map[string]any{"path": path}, nil

	case pluginproto.MethodHostDialogPickDir:
		var request struct {
			Title string `json:"title"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if s.ui == nil {
			return nil, &pluginproto.RemoteError{Code: pluginproto.CodeCapabilityDenied, Message: ErrUnavailable.Error()}
		}
		path, err := s.ui.PickDirectory(ctx, request.Title)
		if err != nil {
			return nil, wrap(err)
		}
		return map[string]any{"path": path}, nil

	case pluginproto.MethodHostNotify:
		var request struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if s.ui == nil {
			return nil, &pluginproto.RemoteError{Code: pluginproto.CodeCapabilityDenied, Message: ErrUnavailable.Error()}
		}
		if err := s.ui.Notify(ctx, request.Title, request.Body); err != nil {
			return nil, wrap(err)
		}
		return map[string]any{}, nil

	case pluginproto.MethodHostElevate:
		var request tool.ElevateRequest
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if s.ui == nil {
			return nil, &pluginproto.RemoteError{Code: pluginproto.CodeCapabilityDenied, Message: ErrUnavailable.Error()}
		}
		result, err := s.ui.Elevate(ctx, request)
		if err != nil {
			return nil, wrap(err)
		}
		return result, nil

	case pluginproto.MethodHostSecretsGet:
		var request struct {
			Name string `json:"name"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		value, err := s.readSecret(toolID, request.Name)
		if err != nil {
			return nil, wrap(err)
		}
		return map[string]any{"value": value}, nil

	case pluginproto.MethodHostSecretsSet:
		var request struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if err := s.writeSecret(toolID, request.Name, request.Value); err != nil {
			return nil, wrap(err)
		}
		return map[string]any{}, nil

	case pluginproto.MethodHostSecretsDelete:
		var request struct {
			Name string `json:"name"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if err := s.deleteSecret(toolID, request.Name); err != nil {
			return nil, wrap(err)
		}
		return map[string]any{}, nil

	case pluginproto.MethodHostConfigGet:
		var request struct {
			Key string `json:"key"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		value, found, err := s.readConfig(toolID, request.Key)
		if err != nil {
			return nil, wrap(err)
		}
		return map[string]any{"value": value, "found": found}, nil

	case pluginproto.MethodHostConfigSet:
		var request struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if err := s.writeConfig(toolID, request.Key, request.Value); err != nil {
			return nil, wrap(err)
		}
		return map[string]any{}, nil
	}
	return nil, &pluginproto.RemoteError{Code: pluginproto.CodeMethodNotFound, Message: "unhandled host method"}
}

func (s *Service) allows(toolID string, capability tool.Capability) bool {
	if s.required == nil {
		return false
	}
	required, ok := s.required(toolID)
	if !ok {
		return false
	}
	for _, name := range required {
		if name == capability {
			return true
		}
	}
	return false
}

func decode(params json.RawMessage, target any) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, target); err != nil {
		return &pluginproto.RemoteError{Code: pluginproto.CodeInvalidParams, Message: "invalid host params: " + err.Error()}
	}
	return nil
}

// wrap turns an internal failure into a protocol error without leaking stack
// traces to the tool.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var remote *pluginproto.RemoteError
	if errors.As(err, &remote) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return &pluginproto.RemoteError{Code: pluginproto.CodeCanceled, Message: "canceled"}
	}
	return &pluginproto.RemoteError{Code: pluginproto.CodeToolError, Message: err.Error()}
}

// toolDir is the per-tool directory holding config and secrets.
func (s *Service) toolDir(toolID string) (string, error) {
	name := sanitizeID(toolID)
	if name == "" {
		return "", errors.New("tool id is empty")
	}
	dir := filepath.Join(s.dataDir, "tools", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create tool state directory: %w", err)
	}
	return dir, nil
}

func sanitizeID(id string) string {
	var builder strings.Builder
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '.' || char == '-' || char == '_':
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func (s *Service) readConfig(toolID, key string) (any, bool, error) {
	dir, err := s.toolDir(toolID)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, false, fmt.Errorf("decode config: %w", err)
	}
	raw, ok := document[key]
	if !ok {
		return nil, false, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *Service) writeConfig(toolID, key string, value json.RawMessage) error {
	dir, err := s.toolDir(toolID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	document := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("decode config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	document[key] = value
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data, 0o600)
}

// writeAtomic writes a file through a temporary sibling so a crash never
// leaves a half-written config or secret store.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tempPath, mode)
	return os.Rename(tempPath, path)
}

var _ io.Closer = (*os.File)(nil)
