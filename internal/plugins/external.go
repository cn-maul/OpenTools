// Package plugins discovers downloaded tool executables and adapts their
// JSON-RPC process protocol to the host tool contract.
package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"opentools/pkg/tool"
	"opentools/pkg/tool/pluginproto"
)

const (
	maxProtocolMessage = 16 << 20
	startupTimeout     = 15 * time.Second
)

// ExternalTool adapts one downloadable executable to the host tool contract.
type ExternalTool struct {
	definition tool.Definition
	executable string
	protocol   int
	nextID     atomic.Int64
}

// NewExternalTool returns a tool backed by an executable.
func NewExternalTool(def tool.Definition, executable string, protocol int) *ExternalTool {
	t := &ExternalTool{
		definition: def,
		executable: executable,
		protocol:   protocol,
	}
	t.nextID.Store(0)
	return t
}

// Definition implements tool.Tool.
func (t *ExternalTool) Definition() tool.Definition {
	return t.definition
}

// Run implements tool.Tool. Cancellation terminates the child process.
func (t *ExternalTool) Run(
	ctx context.Context,
	input tool.Input,
	env tool.Environment,
	report tool.Reporter,
) (tool.Result, error) {
	cmd := exec.CommandContext(ctx, t.executable, "rpc")
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

	var stderrBuffer limitedBuffer
	stderrBuffer.limit = 128 << 10
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderrBuffer, stderr)
	}()

	encoder := json.NewEncoder(stdin)
	call := rpcCaller{
		decoder:    json.NewDecoder(bufio.NewReaderSize(stdout, 64<<10)),
		encoder:    encoder,
		report:     report,
		nextID:     &t.nextID,
		executable: t.executable,
	}

	cleanup := func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		<-stderrDone
	}

	initCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	initParams := pluginproto.InitializeParams{
		ProtocolVersion: t.protocol,
		ToolID:          t.definition.ID,
		WorkDir:         env.WorkDir,
		DataDir:         env.DataDir,
	}
	var initialized pluginproto.InitializeResult
	if err := call.request(initCtx, pluginproto.MethodInitialize, initParams, &initialized); err != nil {
		cancel()
		cleanup()
		if errors.Is(ctx.Err(), context.Canceled) {
			return tool.Result{}, context.Canceled
		}
		if detail := strings.TrimSpace(stderrBuffer.String()); detail != "" {
			return tool.Result{}, fmt.Errorf("%w: %s", err, detail)
		}
		return tool.Result{}, err
	}
	cancel()
	if initialized.ProtocolVersion != t.protocol || initialized.ToolID != t.definition.ID {
		cleanup()
		return tool.Result{}, fmt.Errorf(
			"tool process identity mismatch: protocol=%d tool=%q",
			initialized.ProtocolVersion,
			initialized.ToolID,
		)
	}

	var result tool.Result
	runErr := call.request(ctx, pluginproto.MethodRun, pluginproto.RunParams{Input: input.Values}, &result)
	_ = stdin.Close()
	if errors.Is(ctx.Err(), context.Canceled) {
		_ = cmd.Process.Kill()
		<-stderrDone
		return tool.Result{}, context.Canceled
	}
	waitErr := cmd.Wait()
	<-stderrDone
	if runErr != nil {
		if detail := strings.TrimSpace(stderrBuffer.String()); detail != "" {
			return tool.Result{}, fmt.Errorf("%w: %s", runErr, detail)
		}
		return tool.Result{}, runErr
	}
	if waitErr != nil {
		if detail := strings.TrimSpace(stderrBuffer.String()); detail != "" {
			return tool.Result{}, fmt.Errorf("tool process failed: %s", detail)
		}
		return tool.Result{}, fmt.Errorf("tool process failed: %w", waitErr)
	}
	if err := tool.ValidateResult(result); err != nil {
		return tool.Result{}, fmt.Errorf("invalid tool result: %w", err)
	}
	return result, nil
}

type rpcCaller struct {
	decoder    *json.Decoder
	encoder    *json.Encoder
	report     tool.Reporter
	nextID     *atomic.Int64
	executable string
}

func (c *rpcCaller) request(
	ctx context.Context,
	method string,
	params any,
	target any,
) error {
	// IDs must be positive and non-zero for JSON-RPC.
	id := c.nextID.Add(1)
	data, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	if err := c.encoder.Encode(pluginproto.Request{
		JSONRPC: pluginproto.JSONRPCVersion,
		ID:      &id,
		Method:  method,
		Params:  data,
	}); err != nil {
		return fmt.Errorf("write %s request: %w", method, err)
	}

	type decoded struct {
		response pluginproto.Response
		err      error
	}
	// json.Decoder blocks outside the context. The child process is killed by
	// CommandContext, which closes stdout and unblocks the goroutine.
	ch := make(chan decoded, 1)
	go func() {
		for {
			var response pluginproto.Response
			raw, readErr := readJSONObject(c.decoder)
			if readErr != nil {
				ch <- decoded{err: fmt.Errorf("read %s response: %w", method, readErr)}
				return
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				ch <- decoded{err: fmt.Errorf("decode %s response: %w", method, err)}
				return
			}
			if response.JSONRPC != pluginproto.JSONRPCVersion {
				ch <- decoded{err: fmt.Errorf("tool returned invalid JSON-RPC version")}
				return
			}
			if response.ID == nil {
				var notification pluginproto.Request
				if err := json.Unmarshal(raw, &notification); err != nil {
					ch <- decoded{err: fmt.Errorf("decode tool notification: %w", err)}
					return
				}
				if err := c.handleNotification(notification); err != nil {
					ch <- decoded{err: err}
					return
				}
				continue
			}
			if *response.ID != id {
				ch <- decoded{err: fmt.Errorf("unexpected response id %d (want %d)", *response.ID, id)}
				return
			}
			ch <- decoded{response: response}
			return
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case item := <-ch:
		if item.err != nil {
			return item.err
		}
		if item.response.Error != nil {
			return errors.New(item.response.Error.Message)
		}
		if target == nil {
			return nil
		}
		if len(item.response.Result) == 0 {
			return fmt.Errorf("%s returned no result", method)
		}
		if err := json.Unmarshal(item.response.Result, target); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

func (c *rpcCaller) handleNotification(request pluginproto.Request) error {
	switch request.Method {
	case pluginproto.MethodLog:
		var params pluginproto.LogParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return fmt.Errorf("decode tool log: %w", err)
		}
		c.report.Log(params.Level, params.Message)
	case pluginproto.MethodProgress:
		var params pluginproto.ProgressParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return fmt.Errorf("decode tool progress: %w", err)
		}
		c.report.Progress(params.Done, params.Total)
	case pluginproto.MethodSection:
		var params pluginproto.SectionParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return fmt.Errorf("decode tool section: %w", err)
		}
		if err := tool.ValidateSection(params.Section); err != nil {
			return fmt.Errorf("invalid tool section: %w", err)
		}
		c.report.Section(params.Section)
	default:
		return fmt.Errorf("tool %s sent unsupported notification %q", c.executable, request.Method)
	}
	return nil
}

func readJSONObject(decoder *json.Decoder) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw) > maxProtocolMessage {
		return nil, fmt.Errorf("protocol message exceeds %d bytes", maxProtocolMessage)
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, errors.New("protocol message is not a JSON object")
	}
	return raw, nil
}

type limitedBuffer struct {
	data  bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.data.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.data.Write(data)
	}
	return original, nil
}

func (b *limitedBuffer) String() string {
	return b.data.String()
}

var _ tool.Tool = (*ExternalTool)(nil)
