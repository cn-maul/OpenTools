// Package plugins discovers downloaded tool executables and adapts their
// JSON-RPC process protocol to the host tool contract.
package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cn-maul/OpenTools/pkg/tool"
	"github.com/cn-maul/OpenTools/pkg/tool/pluginproto"
)

const (
	startupTimeout = 15 * time.Second
	// cancelGrace is how long a tool has to stop on its own after "$/cancel"
	// before the process is terminated.
	cancelGrace = 3 * time.Second
	// maxConsoleLine caps one line of tool stderr kept for diagnostics.
	maxConsoleLine = 8 << 10
)

// ExternalTool adapts one downloadable executable to the host tool contract.
type ExternalTool struct {
	definition tool.Definition
	executable string
	protocol   int
	services   tool.HostServices
}

// NewExternalTool returns a tool backed by an executable. services may be nil,
// in which case every host call the tool makes is rejected.
func NewExternalTool(def tool.Definition, executable string, protocol int, services tool.HostServices) *ExternalTool {
	return &ExternalTool{
		definition: def,
		executable: executable,
		protocol:   protocol,
		services:   services,
	}
}

// Definition implements tool.Tool.
func (t *ExternalTool) Definition() tool.Definition {
	return t.definition
}

// grantedCapabilities is the set the host can actually serve. The tool
// intersects it with its own Definition.Requires during initialize.
func (t *ExternalTool) grantedCapabilities() []string {
	if provider, ok := t.services.(interface{ Capabilities() []string }); ok {
		return provider.Capabilities()
	}
	return nil
}

// Run implements tool.Tool.
//
// Cancellation is graceful first: the host sends "$/cancel", waits
// cancelGrace for the tool to answer, and only then terminates the process.
// A tool that honors the notification can flush artifacts and roll back.
func (t *ExternalTool) Run(
	ctx context.Context,
	input tool.Input,
	env tool.Environment,
	report tool.Reporter,
) (tool.Result, error) {
	cmd := exec.Command(t.executable, "rpc")
	cmd.Dir = env.WorkDir
	cmd.WaitDelay = 2 * time.Second
	if err := hideWindow(cmd); err != nil {
		return tool.Result{}, fmt.Errorf("prepare tool process: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return tool.Result{}, fmt.Errorf("open tool stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return tool.Result{}, fmt.Errorf("open tool stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return tool.Result{}, fmt.Errorf("open tool stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return tool.Result{}, fmt.Errorf("start tool process: %w", err)
	}
	// One goroutine owns cmd.Wait so both the normal path and the
	// escalation path wait on the same result.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var console consoleBuffer
	console.report = report
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		console.consume(stderr)
	}()

	conn := pluginproto.NewConn(stdout, stdin)
	conn.SetMaxMessageBytes(pluginproto.DefaultMaxMessageBytes)
	conn.OnNotification = func(method string, params json.RawMessage) {
		handleNotification(method, params, report)
	}
	conn.OnRequest = t.hostRequest

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- conn.Serve()
	}()

	var closeOnce sync.Once
	closeStdin := func() {
		closeOnce.Do(func() { _ = stdin.Close() })
	}
	var killOnce sync.Once
	kill := func() {
		killOnce.Do(func() { _ = cmd.Process.Kill() })
	}

	// A single supervisor owns the process outcome, so exactly one goroutine
	// calls Wait and exactly one kill decision reaches the OS.
	procDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			// Notify first, then close stdin: EOF is an independent stop
			// signal for tools that do not read notifications. Closing
			// stdin before the notification would make the write fail and
			// the tool would never see "$/cancel".
			_ = conn.Notify(pluginproto.MethodCancel, pluginproto.CancelParams{Reason: "host requested cancellation"})
			closeStdin()
			timer := time.NewTimer(cancelGrace)
			defer timer.Stop()
			select {
			case err := <-waitDone:
				procDone <- err
			case <-timer.C:
				kill()
				procDone <- <-waitDone
			}
		case err := <-waitDone:
			procDone <- err
		}
	}()

	initCtx, cancelInit := context.WithTimeout(ctx, startupTimeout)
	initParams := pluginproto.InitializeParams{
		ProtocolVersion:     t.protocol,
		ContractVersion:     pluginproto.ContractVersion,
		ToolID:              t.definition.ID,
		WorkDir:             env.WorkDir,
		DataDir:             env.DataDir,
		GrantedCapabilities: t.grantedCapabilities(),
		Limits: pluginproto.Limits{
			MaxMessageBytes: pluginproto.DefaultMaxMessageBytes,
			CancelGraceMS:   cancelGrace.Milliseconds(),
		},
	}
	var initialized pluginproto.InitializeResult
	initErr := conn.Request(initCtx, pluginproto.MethodInitialize, initParams, &initialized)
	cancelInit()
	if initErr != nil {
		kill()
		<-procDone
		<-stderrDone
		if errors.Is(ctx.Err(), context.Canceled) {
			return tool.Result{}, context.Canceled
		}
		if detail := console.String(); detail != "" {
			return tool.Result{}, fmt.Errorf("%w: %s", initErr, detail)
		}
		return tool.Result{}, initErr
	}
	if initialized.ProtocolVersion != t.protocol || initialized.ToolID != t.definition.ID {
		kill()
		<-procDone
		<-stderrDone
		return tool.Result{}, fmt.Errorf(
			"tool process identity mismatch: protocol=%d tool=%q",
			initialized.ProtocolVersion,
			initialized.ToolID,
		)
	}

	var result tool.Result
	runErr := conn.Request(ctx, pluginproto.MethodRun, pluginproto.RunParams{Input: input.Values}, &result)

	// Closing stdin ends the tool's read loop, which is the normal exit
	// path. When the caller canceled, the supervisor does this itself right
	// after the notification; closing here would race that write.
	if ctx.Err() == nil {
		closeStdin()
	}
	var waitErr error
	select {
	case waitErr = <-procDone:
	case <-time.After(cancelGrace + 2*time.Second):
		kill()
		waitErr = <-procDone
	}
	waitFor(serveDone, stderrDone)
	console.flush()

	if errors.Is(ctx.Err(), context.Canceled) {
		return tool.Result{}, context.Canceled
	}
	if runErr != nil {
		var remote *pluginproto.RemoteError
		if errors.As(runErr, &remote) {
			switch remote.Code {
			case pluginproto.CodeCanceled:
				return tool.Result{}, context.Canceled
			case pluginproto.CodeToolError:
				return tool.Result{}, errors.New(remote.Message)
			}
		}
		if detail := console.String(); detail != "" {
			return tool.Result{}, fmt.Errorf("%w: %s", runErr, detail)
		}
		return tool.Result{}, runErr
	}
	if waitErr != nil {
		if detail := console.String(); detail != "" {
			return tool.Result{}, fmt.Errorf("tool process failed: %s", detail)
		}
		return tool.Result{}, fmt.Errorf("tool process failed: %w", waitErr)
	}
	if err := tool.ValidateResult(result); err != nil {
		return tool.Result{}, fmt.Errorf("invalid tool result: %w", err)
	}
	return result, nil
}

// hostRequest answers a call the tool made into the host.
func (t *ExternalTool) hostRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if err := pluginproto.CheckGranted(t.definition.Requires, method); err != nil {
		return nil, err
	}
	if t.services == nil {
		return nil, &pluginproto.RemoteError{
			Code:    pluginproto.CodeCapabilityDenied,
			Message: "this host grants no capabilities",
		}
	}
	var decoded any
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &decoded); err != nil {
			return nil, &pluginproto.RemoteError{Code: pluginproto.CodeInvalidParams, Message: "invalid host params"}
		}
	}
	raw, err := json.Marshal(decoded)
	if err != nil {
		return nil, &pluginproto.RemoteError{Code: pluginproto.CodeInvalidParams, Message: "invalid host params"}
	}
	return t.services.Handle(ctx, t.definition.ID, method, raw)
}

// waitFor drains the remaining pipes after the process exited.
func waitFor(serveDone <-chan error, stderrDone <-chan struct{}) {
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
	}
	select {
	case <-stderrDone:
	case <-time.After(2 * time.Second):
	}
}

func handleNotification(method string, params json.RawMessage, report tool.Reporter) {
	// A missing reporter must not turn an incoming notification into a panic:
	// this runs on the connection reader goroutine, so a panic there would
	// take the whole host down.
	if report == nil {
		return
	}
	switch method {
	case pluginproto.MethodLog:
		var decoded pluginproto.LogParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return
		}
		report.Log(decoded.Level, decoded.Message)
	case pluginproto.MethodProgress:
		var decoded pluginproto.ProgressParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return
		}
		report.Progress(decoded.Done, decoded.Total)
	case pluginproto.MethodSection:
		var decoded pluginproto.SectionParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return
		}
		if tool.ValidateSection(decoded.Section) != nil {
			return
		}
		report.Section(decoded.Section)
	default:
		// A newer tool may send notifications this host does not know. That
		// is forward compatibility, not an error: drop it and keep going.
	}
}

// consoleBuffer forwards tool stderr to the job log line by line while
// keeping a bounded tail for error messages.
type consoleBuffer struct {
	mu     sync.Mutex
	data   bytes.Buffer
	line   bytes.Buffer
	report tool.Reporter
}

func (b *consoleBuffer) consume(reader io.Reader) {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			b.write(buffer[:n])
		}
		if err != nil {
			b.flush()
			return
		}
	}
}

func (b *consoleBuffer) write(data []byte) {
	b.mu.Lock()
	var lines []string
	for _, char := range data {
		if char == '\n' {
			lines = append(lines, strings.TrimRight(b.line.String(), "\r"))
			b.line.Reset()
			continue
		}
		if b.line.Len() < maxConsoleLine {
			b.line.WriteByte(char)
		}
	}
	if b.data.Len() < maxConsoleLine*4 {
		b.data.Write(data)
	}
	report := b.report
	b.mu.Unlock()
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if report != nil {
			report.Log("stderr", line)
		}
	}
}

func (b *consoleBuffer) flush() {
	b.mu.Lock()
	line := strings.TrimSpace(b.line.String())
	b.line.Reset()
	report := b.report
	b.mu.Unlock()
	if line != "" && report != nil {
		report.Log("stderr", line)
	}
}

func (b *consoleBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.data.String())
}

var _ tool.Tool = (*ExternalTool)(nil)
