// Package host provides typed helpers for the host services a tool may call.
//
// Every helper is a thin wrapper over tool.Environment.Host, which is the
// only primitive. A tool that runs without a host gets a clear error instead
// of a silent no-op, so it can degrade explicitly.
package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/cn-maul/OpenTools/sdk/tool"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

// ErrNoHost is returned when a tool tries to use a service while running
// outside an OpenTools host.
var ErrNoHost = errors.New("no OpenTools host is connected")

// Secrets stores a short secret per tool, keyed by name. Values never become
// part of a job snapshot; the host keeps them out of the UI and database.
type Secrets struct {
	host tool.Host
}

// OpenSecrets returns the secrets service for env. It fails when the tool did
// not declare the "secrets" capability.
func OpenSecrets(env tool.Environment) (*Secrets, error) {
	if env.Host == nil {
		return nil, ErrNoHost
	}
	if !env.Host.Granted(pluginproto.CapabilitySecrets) {
		return nil, capabilityError(pluginproto.CapabilitySecrets)
	}
	return &Secrets{host: env.Host}, nil
}

// Get returns the stored secret, or "" when it was never set.
func (s *Secrets) Get(ctx context.Context, name string) (string, error) {
	var result struct {
		Value string `json:"value"`
	}
	if err := s.host.Call(ctx, pluginproto.MethodHostSecretsGet, map[string]any{"name": name}, &result); err != nil {
		return "", err
	}
	return result.Value, nil
}

// Set stores a secret for this tool.
func (s *Secrets) Set(ctx context.Context, name, value string) error {
	return s.host.Call(ctx, pluginproto.MethodHostSecretsSet, map[string]any{"name": name, "value": value}, nil)
}

// Delete removes a stored secret.
func (s *Secrets) Delete(ctx context.Context, name string) error {
	return s.host.Call(ctx, pluginproto.MethodHostSecretsDelete, map[string]any{"name": name}, nil)
}

// Config stores small, non-secret settings per tool.
type Config struct {
	host tool.Host
}

// OpenConfig returns the config service for env.
func OpenConfig(env tool.Environment) (*Config, error) {
	if env.Host == nil {
		return nil, ErrNoHost
	}
	if !env.Host.Granted(pluginproto.CapabilityConfig) {
		return nil, capabilityError(pluginproto.CapabilityConfig)
	}
	return &Config{host: env.Host}, nil
}

// Get decodes the stored value into target. A missing key leaves target as
// it is and reports found=false.
func (c *Config) Get(ctx context.Context, key string, target any) (found bool, err error) {
	var result struct {
		Value any  `json:"value"`
		Found bool `json:"found"`
	}
	if err := c.host.Call(ctx, pluginproto.MethodHostConfigGet, map[string]any{"key": key}, &result); err != nil {
		return false, err
	}
	if !result.Found || target == nil {
		return result.Found, nil
	}
	if err := remarshal(result.Value, target); err != nil {
		return true, fmt.Errorf("decode config %q: %w", key, err)
	}
	return true, nil
}

// Set stores a JSON-encodable value.
func (c *Config) Set(ctx context.Context, key string, value any) error {
	return c.host.Call(ctx, pluginproto.MethodHostConfigSet, map[string]any{"key": key, "value": value}, nil)
}

// Dialogs opens native pickers. The tool never sees a path the user did not
// choose.
type Dialogs struct {
	host tool.Host
}

// OpenDialogs returns the dialog service for env.
func OpenDialogs(env tool.Environment) (*Dialogs, error) {
	if env.Host == nil {
		return nil, ErrNoHost
	}
	if !env.Host.Granted(pluginproto.CapabilityDialogs) {
		return nil, capabilityError(pluginproto.CapabilityDialogs)
	}
	return &Dialogs{host: env.Host}, nil
}

// OpenFile shows an open dialog and returns one path, or "" when canceled.
func (d *Dialogs) OpenFile(ctx context.Context, title string, extensions []string) (string, error) {
	var result struct {
		Path string `json:"path"`
	}
	params := map[string]any{"title": title, "extensions": extensions}
	if err := d.host.Call(ctx, pluginproto.MethodHostDialogOpenFile, params, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// SaveFile shows a save dialog and returns the chosen path, or "" when
// canceled.
func (d *Dialogs) SaveFile(ctx context.Context, title, suggested string, extensions []string) (string, error) {
	var result struct {
		Path string `json:"path"`
	}
	params := map[string]any{"title": title, "suggested": suggested, "extensions": extensions}
	if err := d.host.Call(ctx, pluginproto.MethodHostDialogSaveFile, params, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// PickDirectory shows a folder picker and returns the chosen path.
func (d *Dialogs) PickDirectory(ctx context.Context, title string) (string, error) {
	var result struct {
		Path string `json:"path"`
	}
	if err := d.host.Call(ctx, pluginproto.MethodHostDialogPickDir, map[string]any{"title": title}, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// Notify posts a system notification.
func Notify(ctx context.Context, env tool.Environment, title, body string) error {
	if env.Host == nil {
		return ErrNoHost
	}
	if !env.Host.Granted(pluginproto.CapabilityNotify) {
		return capabilityError(pluginproto.CapabilityNotify)
	}
	return env.Host.Call(ctx, pluginproto.MethodHostNotify, map[string]any{"title": title, "body": body}, nil)
}

// Elevate runs one command with administrator rights through the host, which
// owns the UAC prompt. Tools never implement elevation themselves.
func Elevate(ctx context.Context, env tool.Environment, params tool.ElevateRequest) (tool.ElevateResult, error) {
	var result tool.ElevateResult
	if env.Host == nil {
		return result, ErrNoHost
	}
	if !env.Host.Granted(pluginproto.CapabilityElevate) {
		return result, capabilityError(pluginproto.CapabilityElevate)
	}
	err := env.Host.Call(ctx, pluginproto.MethodHostElevate, params, &result)
	return result, err
}

// capabilityError explains why a host service is unavailable. The two causes
// need different fixes: declaring the capability in the tool definition, or
// running in a host that implements it.
func capabilityError(capability tool.Capability) error {
	return fmt.Errorf(
		"host service %q is unavailable: the host did not grant it (declare it in Definition.Requires, and run inside a host that implements it)",
		capability,
	)
}
