package pluginproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"opentools/pkg/tool"
)

// Serve runs the command-line contract for a downloadable tool executable.
//
// Supported modes:
//   - describe: print the tool definition as JSON
//   - rpc: serve line-delimited JSON-RPC 2.0 requests on stdio
func Serve(implementation tool.Tool) {
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
		if err := serveRPC(implementation, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", os.Args[1])
		os.Exit(2)
	}
}

func serveRPC(implementation tool.Tool, input io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(input)
	encoder := json.NewEncoder(output)
	initialized := false
	var current session

	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode JSON-RPC request: %w", err)
		}
		if request.JSONRPC != JSONRPCVersion {
			if request.ID != nil {
				if err := writeError(encoder, request.ID, CodeInvalidRequest, "jsonrpc must be 2.0", nil); err != nil {
					return err
				}
			}
			continue
		}

		switch request.Method {
		case MethodInitialize:
			if request.ID == nil {
				continue
			}
			result, session, err := initialize(implementation, request.Params)
			if err != nil {
				if encodeErr := writeError(encoder, request.ID, CodeInvalidParams, err.Error(), nil); encodeErr != nil {
					return encodeErr
				}
				continue
			}
			current = session
			initialized = true
			if err := writeResult(encoder, request.ID, result); err != nil {
				return err
			}
		case MethodRun:
			if request.ID == nil {
				continue
			}
			if !initialized {
				if err := writeError(encoder, request.ID, CodeInvalidRequest, "initialize must be called first", nil); err != nil {
					return err
				}
				continue
			}
			if err := run(implementation, encoder, request, current); err != nil {
				return err
			}
		default:
			if request.ID != nil {
				if err := writeError(encoder, request.ID, CodeMethodNotFound, "method not found", nil); err != nil {
					return err
				}
			}
		}
	}
}

type session struct {
	protocol int
	workDir  string
	dataDir  string
}

func initialize(implementation tool.Tool, raw json.RawMessage) (InitializeResult, session, error) {
	var params InitializeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return InitializeResult{}, session{}, fmt.Errorf("decode initialize params: %w", err)
	}
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
	return InitializeResult{
		ProtocolVersion: params.ProtocolVersion,
		ToolID:          def.ID,
		Version:         def.Version,
	}, session{
		protocol: params.ProtocolVersion,
		workDir:  params.WorkDir,
		dataDir:  params.DataDir,
	}, nil
}

func run(
	implementation tool.Tool,
	encoder *json.Encoder,
	request Request,
	current session,
) error {
	var params RunParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return writeError(encoder, request.ID, CodeInvalidParams, "invalid run params", err.Error())
	}
	if params.Input == nil {
		params.Input = map[string]any{}
	}
	reporter := processReporter{encoder: encoder, protocol: current.protocol}

	result, runErr := callTool(implementation, params.Input, current, reporter)
	if runErr != nil {
		return writeError(encoder, request.ID, CodeToolError, runErr.Error(), nil)
	}
	if err := tool.ValidateResult(result); err != nil {
		return writeError(encoder, request.ID, CodeInternalError, "invalid tool result", err.Error())
	}
	return writeResult(encoder, request.ID, result)
}

func callTool(
	implementation tool.Tool,
	input map[string]any,
	current session,
	reporter tool.Reporter,
) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool panicked: %v", recovered)
		}
	}()
	return implementation.Run(
		context.Background(),
		tool.Input{Values: input},
		tool.Environment{WorkDir: current.workDir, DataDir: current.dataDir},
		reporter,
	)
}

func writeResult(encoder *json.Encoder, id *int64, result any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return writeError(encoder, id, CodeInternalError, "encode result", err.Error())
	}
	return encoder.Encode(Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  data,
	})
}

func writeError(encoder *json.Encoder, id *int64, code int, message string, data any) error {
	return encoder.Encode(Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	})
}

type processReporter struct {
	encoder  *json.Encoder
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
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return r.encoder.Encode(Request{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  data,
	})
}
