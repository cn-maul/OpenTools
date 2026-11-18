package pluginproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cn-maul/OpenTools/pkg/tool"
)

// Serve runs the command-line contract for a downloadable tool executable.
//
// Supported modes:
//   - describe: print the tool definition as JSON
//   - rpc: serve line-delimited JSON-RPC 2.0 requests on stdio
func Serve(implementation tool.Tool) {
	ServeWithHost(implementation, nil)
}

// ServeWithHost runs the command-line contract with host services available
// to the tool. A nil client keeps the tool self-contained: it can still
// describe itself and run, but every host call fails.
func ServeWithHost(implementation tool.Tool, services HostClient) {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: <tool> describe|rpc")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "describe":
		if err := json.NewEncoder(os.Stdout).Encode(implementation.Definition()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "rpc":
		if err := serveRPC(implementation, services, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", os.Args[1])
		os.Exit(2)
	}
}

func serveRPC(implementation tool.Tool, services HostClient, input io.Reader, output io.Writer) error {
	conn := NewConn(input, output)
	state := &serveState{implementation: implementation, conn: conn, host: newHostClient(conn, services)}
	state.idle = sync.NewCond(&state.mu)
	conn.OnRequest = state.onRequest
	conn.OnNotification = state.onNotification
	if err := conn.Serve(); err != nil {
		return err
	}
	// EOF on stdin is a stop signal: the host is gone or has stopped
	// waiting. Cancel a running invocation so it unwinds instead of being
	// terminated after the grace period.
	state.cancelActive()
	state.wait()
	return nil
}

type session struct {
	protocol int
	workDir  string
	dataDir  string
	granted  []string
	limits   tool.Limits
}

// serveState is the mutable state of one stdio session.
type serveState struct {
	implementation tool.Tool
	conn           *Conn
	host           *hostBridge

	mu          sync.Mutex
	initialized bool
	session     session
	running     bool
	cancelRun   context.CancelFunc
	// inflight counts invocations still unwinding so the process can wait for
	// a canceled run to finish before exiting. idle is signaled under mu when
	// it drops to zero.
	inflight int
	idle     *sync.Cond
}

func (s *serveState) onRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case MethodInitialize:
		var decoded InitializeParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return nil, &RemoteError{Code: CodeInvalidParams, Message: "decode initialize params: " + err.Error()}
		}
		result, state, err := initialize(s.implementation, decoded, s.host)
		if err != nil {
			return nil, &RemoteError{Code: CodeInvalidParams, Message: err.Error()}
		}
		s.mu.Lock()
		s.session = state
		s.initialized = true
		s.mu.Unlock()
		return result, nil
	case MethodRun:
		s.mu.Lock()
		initialized := s.initialized
		current := s.session
		alreadyRunning := s.running
		if initialized && !alreadyRunning {
			s.running = true
			s.inflight++
		}
		s.mu.Unlock()
		if !initialized {
			return nil, &RemoteError{Code: CodeInvalidRequest, Message: "initialize must be called first"}
		}
		if alreadyRunning {
			return nil, &RemoteError{Code: CodeInvalidRequest, Message: "a run is already in progress"}
		}
		defer func() {
			s.mu.Lock()
			s.inflight--
			if s.inflight == 0 {
				s.idle.Broadcast()
			}
			s.mu.Unlock()
		}()
		result, err := s.runOnce(current, params)
		s.mu.Lock()
		s.running = false
		session := s.session
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if session.limits.MaxMessageBytes > 0 {
			if size, marshalErr := json.Marshal(result); marshalErr == nil && int64(len(size)) > session.limits.MaxMessageBytes {
				return nil, &RemoteError{
					Code:    CodeInvalidRequest,
					Message: fmt.Sprintf("result is %d bytes, above the %d byte limit; split it into sections", len(size), session.limits.MaxMessageBytes),
				}
			}
		}
		return result, nil
	default:
		return nil, &RemoteError{Code: CodeMethodNotFound, Message: "method not found"}
	}
}

func (s *serveState) onNotification(method string, params json.RawMessage) {
	switch method {
	case MethodCancel:
		s.cancelActive()
	case MethodLog, MethodProgress, MethodSection:
		// Host-bound notifications accidentally sent to a tool are ignored;
		// a malformed peer must not take the session down.
	default:
		// Unknown notifications are forward compatible by design.
	}
}

func (s *serveState) wait() {
	// The host closes stdin once it has the result or has given up, so a
	// canceled run is already unwinding. Waiting here keeps a tool from being
	// killed mid-write.
	s.mu.Lock()
	for s.inflight > 0 {
		s.idle.Wait()
	}
	s.mu.Unlock()
}

// cancelActive cancels the running invocation, if any.
func (s *serveState) cancelActive() {
	s.mu.Lock()
	cancel := s.cancelRun
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func initialize(implementation tool.Tool, params InitializeParams, host *hostBridge) (InitializeResult, session, error) {
	if !SupportsVersion(params.ProtocolVersion) {
		return InitializeResult{}, session{}, fmt.Errorf(
			"unsupported protocol version %d; executable supports %d-%d",
			params.ProtocolVersion,
			MinimumVersion,
			Version,
		)
	}
	def := implementation.Definition()
	if params.ToolID != "" && params.ToolID != def.ID {
		return InitializeResult{}, session{}, fmt.Errorf(
			"tool id mismatch: request %q, executable %q",
			params.ToolID,
			def.ID,
		)
	}
	granted := intersect(def.Requires, params.GrantedCapabilities)
	host.setGranted(granted, params.Limits)
	return InitializeResult{
		ProtocolVersion: params.ProtocolVersion,
		ContractVersion: ContractVersion,
		ToolID:          def.ID,
		Version:         def.Version,
		Capabilities:    granted,
		Limits:          params.Limits,
	}, session{
		protocol: params.ProtocolVersion,
		workDir:  params.WorkDir,
		dataDir:  params.DataDir,
		granted:  granted,
		limits: tool.Limits{
			MaxMessageBytes: params.Limits.MaxMessageBytes,
			MaxJobDuration:  time.Duration(params.Limits.MaxJobDurationMS) * time.Millisecond,
			CancelGrace:     time.Duration(params.Limits.CancelGraceMS) * time.Millisecond,
		},
	}, nil
}

// runOnce executes the tool once and returns its validated result.
func (s *serveState) runOnce(current session, raw json.RawMessage) (tool.Result, error) {
	var params RunParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return tool.Result{}, &RemoteError{Code: CodeInvalidParams, Message: "invalid run params: " + err.Error()}
	}
	if params.Input == nil {
		params.Input = map[string]any{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	if current.limits.MaxJobDuration > 0 {
		ctx, cancel = context.WithTimeout(ctx, current.limits.MaxJobDuration)
	}
	s.mu.Lock()
	s.cancelRun = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancelRun = nil
		s.mu.Unlock()
		cancel()
	}()

	reporter := processReporter{conn: s.conn, protocol: current.protocol}
	env := tool.Environment{
		WorkDir: current.workDir,
		DataDir: current.dataDir,
		Host:    s.host,
	}
	result, runErr := callTool(ctx, s.implementation, params.Input, env, reporter)
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return tool.Result{}, &RemoteError{Code: CodeCanceled, Message: "tool run was canceled"}
		}
		return tool.Result{}, &RemoteError{Code: CodeToolError, Message: runErr.Error()}
	}
	if err := tool.ValidateResult(result); err != nil {
		return tool.Result{}, &RemoteError{Code: CodeInternalError, Message: "invalid tool result: " + err.Error()}
	}
	return result, nil
}

func callTool(
	ctx context.Context,
	implementation tool.Tool,
	input map[string]any,
	env tool.Environment,
	reporter tool.Reporter,
) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool panicked: %v", recovered)
		}
	}()
	return implementation.Run(
		ctx,
		tool.Input{Values: input},
		env,
		reporter,
	)
}

type processReporter struct {
	conn     *Conn
	protocol int
}

func (r processReporter) Log(level, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	_ = r.notify(MethodLog, LogParams{Level: level, Message: message})
}

func (r processReporter) Progress(done, total int64) {
	_ = r.notify(MethodProgress, ProgressParams{Done: done, Total: total})
}

func (r processReporter) Section(section tool.Section) {
	if r.protocol < 2 {
		return
	}
	if tool.ValidateSection(section) != nil {
		return
	}
	_ = r.notify(MethodSection, SectionParams{Section: section})
}

func (r processReporter) notify(method string, params any) error {
	return r.conn.Notify(method, params)
}

func intersect(required, granted []string) []string {
	if len(required) == 0 {
		return nil
	}
	available := make(map[string]struct{}, len(granted))
	for _, name := range granted {
		available[name] = struct{}{}
	}
	out := make([]string, 0, len(required))
	for _, name := range required {
		if _, ok := available[name]; ok {
			out = append(out, name)
		}
	}
	return out
}
