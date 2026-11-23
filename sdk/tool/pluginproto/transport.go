package pluginproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// DefaultMaxMessageBytes caps one encoded JSON-RPC message in either
// direction when neither side states a limit.
const DefaultMaxMessageBytes = 16 << 20

// ErrHandled tells Conn that the request handler wrote its own response and
// the connection must not write another one. A handler uses it for a call it
// answers asynchronously, such as tool/run.
var ErrHandled = errors.New("request handled by caller")

// RemoteError is a JSON-RPC error raised by the peer. Handlers return it to
// choose the error code; any other error becomes CodeInternalError.
type RemoteError struct {
	Code    int
	Message string
	Data    any
}

func (e *RemoteError) Error() string {
	return e.Message
}

// Conn is a bidirectional JSON-RPC 2.0 connection over a byte stream.
//
// Both peers may call and answer at the same time: the reader demultiplexes
// responses from requests and notifications, while Request may be called
// concurrently from any goroutine. Writes are serialized, so two goroutines
// never interleave half a message.
type Conn struct {
	decoder *json.Decoder
	encoder *json.Encoder

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan *Response
	closed  bool
	err     error

	maxMessage int64

	// OnRequest answers an incoming request. Returning ErrHandled means the
	// handler wrote the response itself; returning a *RemoteError controls
	// the error code.
	OnRequest func(ctx context.Context, method string, params json.RawMessage) (any, error)
	// OnNotification handles a request without an ID. Unknown notifications
	// must be ignored rather than failing the session.
	OnNotification func(method string, params json.RawMessage)
}

// NewConn returns a connection reading from input and writing to output.
func NewConn(input io.Reader, output io.Writer) *Conn {
	return &Conn{
		decoder:    json.NewDecoder(input),
		encoder:    json.NewEncoder(output),
		pending:    map[int64]chan *Response{},
		maxMessage: DefaultMaxMessageBytes,
	}
}

// SetMaxMessageBytes caps one incoming message.
func (c *Conn) SetMaxMessageBytes(limit int64) {
	if limit > 0 {
		c.maxMessage = limit
	}
}

// Serve reads and dispatches messages until the stream ends. Requests are
// answered on their own goroutine so a long handler cannot stall the reader.
func (c *Conn) Serve() error {
	for {
		var raw json.RawMessage
		err := c.decoder.Decode(&raw)
		if err != nil {
			c.shutdown(err)
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read JSON-RPC message: %w", err)
		}
		if int64(len(raw)) > c.maxMessage {
			c.shutdown(errMessageTooLarge)
			return fmt.Errorf("JSON-RPC message exceeds %d bytes", c.maxMessage)
		}

		var header struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *Error          `json:"error"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return fmt.Errorf("decode JSON-RPC message: %w", err)
		}
		if header.JSONRPC != JSONRPCVersion {
			return fmt.Errorf("JSON-RPC version %q is not %q", header.JSONRPC, JSONRPCVersion)
		}

		switch {
		case header.Method != "" && header.ID != nil:
			c.handleRequest(*header.ID, header.Method, header.Params)
		case header.Method != "":
			if c.OnNotification != nil {
				c.OnNotification(header.Method, header.Params)
			}
		case header.ID != nil:
			c.deliver(&Response{
				JSONRPC: JSONRPCVersion,
				ID:      header.ID,
				Result:  header.Result,
				Error:   header.Error,
			})
		default:
			return errors.New("JSON-RPC message has neither method nor id")
		}
	}
}

var errMessageTooLarge = errors.New("message too large")

func (c *Conn) handleRequest(id int64, method string, params json.RawMessage) {
	go func() {
		if c.OnRequest == nil {
			c.writeError(id, CodeMethodNotFound, "method not found", nil)
			return
		}
		result, err := c.OnRequest(context.Background(), method, params)
		if errors.Is(err, ErrHandled) {
			return
		}
		if err != nil {
			var remote *RemoteError
			if errors.As(err, &remote) {
				c.writeError(id, remote.Code, remote.Message, remote.Data)
				return
			}
			c.writeError(id, CodeInternalError, err.Error(), nil)
			return
		}
		c.Respond(id, result, nil)
	}()
}

// Request calls the peer and decodes its result into target, which may be
// nil when the caller ignores the result.
func (c *Conn) Request(ctx context.Context, method string, params, target any) error {
	id := c.nextID.Add(1)
	ch := make(chan *Response, 1)

	c.mu.Lock()
	if c.closed {
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("connection is closed")
		}
		return err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(Request{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  method,
		Params:  mustRaw(params),
	}); err != nil {
		return fmt.Errorf("write %s request: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case response, ok := <-ch:
		if !ok {
			c.mu.Lock()
			err := c.err
			c.mu.Unlock()
			if err == nil {
				err = errors.New("connection closed while waiting for a response")
			}
			return err
		}
		if response.Error != nil {
			return &RemoteError{
				Code:    response.Error.Code,
				Message: response.Error.Message,
				Data:    response.Error.Data,
			}
		}
		if target == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("%s returned no result", method)
		}
		if err := json.Unmarshal(response.Result, target); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

// Notify sends a notification. A nil params sends an empty object.
func (c *Conn) Notify(method string, params any) error {
	return c.write(Request{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  mustRaw(params),
	})
}

// Respond writes the result of a request the caller handled itself.
func (c *Conn) Respond(id int64, result any, err error) {
	if err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) {
			c.writeError(id, remote.Code, remote.Message, remote.Data)
			return
		}
		c.writeError(id, CodeInternalError, err.Error(), nil)
		return
	}
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		c.writeError(id, CodeInternalError, "encode result", marshalErr.Error())
		return
	}
	_ = c.write(Response{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Result:  data,
	})
}

func (c *Conn) writeError(id int64, code int, message string, data any) {
	_ = c.write(Response{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Error:   &Error{Code: code, Message: message, Data: data},
	})
}

func (c *Conn) write(message any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.encoder.Encode(message)
}

func (c *Conn) deliver(response *Response) {
	if response.ID == nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[*response.ID]
	c.mu.Unlock()
	if !ok {
		// A response to a request we already gave up on is stale, not fatal.
		return
	}
	select {
	case ch <- response:
	default:
	}
}

func (c *Conn) shutdown(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = cause
	pending := c.pending
	c.pending = map[int64]chan *Response{}
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

func mustRaw(value any) json.RawMessage {
	if value == nil {
		return json.RawMessage("{}")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("{}")
	}
	return data
}
