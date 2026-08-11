// Package jsonrpc implements the newline-delimited JSON-RPC 2.0 connection
// shared by the Router and Provider Processes.
package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

const cancelMethod = "$/cancelRequest"

// CodeInvalidParams is the standard JSON-RPC invalid-parameters error code.
const CodeInvalidParams = -32602

// ErrClosed indicates that a call used a closed JSON-RPC connection.
var ErrClosed = errors.New("jsonrpc connection closed")

// Handler processes one JSON-RPC request.
type Handler func(context.Context, []byte) (any, error)

// NotificationHandler processes one JSON-RPC notification.
type NotificationHandler func(context.Context, []byte)

type requestIDKey struct{}

// RequestID returns the JSON-RPC ID associated with a server handler context.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RPCError is a JSON-RPC error response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// InvalidParams wraps a parameter decoding or validation failure.
func InvalidParams(err error) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: err.Error()}
}

func (e *RPCError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

// Conn is a concurrent bidirectional JSON-RPC connection.
type Conn struct {
	decoder *json.Decoder
	encoder *json.Encoder

	writeMu sync.Mutex
	mu      sync.Mutex
	methods map[string]Handler
	notices map[string]NotificationHandler
	pending map[string]chan response
	active  map[string]context.CancelFunc
	nextID  atomic.Uint64

	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// New creates a connection over separate or shared streams.
func New(reader io.Reader, writer io.Writer) *Conn {
	return &Conn{
		decoder: json.NewDecoder(reader),
		encoder: json.NewEncoder(writer),
		methods: map[string]Handler{},
		notices: map[string]NotificationHandler{},
		pending: map[string]chan response{},
		active:  map[string]context.CancelFunc{},
		done:    make(chan struct{}),
	}
}

// Handle registers a request handler for method.
func (c *Conn) Handle(method string, handler Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.methods[method] = handler
}

// OnNotification registers an ordered notification handler for method.
func (c *Conn) OnNotification(method string, handler NotificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notices[method] = handler
}

// Serve reads messages until ctx ends or the peer closes the stream. Calls
// are handled concurrently; notifications stay ordered on the reader loop.
func (c *Conn) Serve(ctx context.Context) error {
	for {
		var msg message
		if err := c.decoder.Decode(&msg); err != nil {
			c.closeWith(err)
			return err
		}
		if msg.JSONRPC != "2.0" {
			err := &RPCError{Code: -32600, Message: "invalid JSON-RPC version"}
			c.closeWith(err)
			return err
		}
		switch {
		case msg.Method != "" && len(msg.ID) != 0:
			go c.handleCall(ctx, msg)
		case msg.Method != "":
			c.handleNotification(ctx, msg)
		default:
			c.handleResponse(msg)
		}
	}
}

// Call invokes a remote method and decodes its result.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	key := strconv.FormatUint(id, 10)
	paramsJSON, err := marshal(params)
	if err != nil {
		return err
	}
	replies := make(chan response, 1)
	c.mu.Lock()
	c.pending[key] = replies
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	if err := c.write(ctx, message{JSONRPC: "2.0", ID: json.RawMessage(key), Method: method, Params: paramsJSON}); err != nil {
		return err
	}
	select {
	case reply := <-replies:
		if reply.err != nil {
			return reply.err
		}
		if result == nil || len(reply.result) == 0 || string(reply.result) == "null" {
			return nil
		}
		return json.Unmarshal(reply.result, result)
	case <-ctx.Done():
		_ = c.notify(context.Background(), cancelMethod, struct {
			ID json.RawMessage `json:"id"`
		}{ID: json.RawMessage(key)})
		return ctx.Err()
	case <-c.done:
		return c.closedError()
	}
}

// Notify sends a notification without awaiting a response.
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	return c.notify(ctx, method, params)
}

func (c *Conn) notify(ctx context.Context, method string, params any) error {
	paramsJSON, err := marshal(params)
	if err != nil {
		return err
	}
	return c.write(ctx, message{JSONRPC: "2.0", Method: method, Params: paramsJSON})
}

// Close unblocks pending calls and closes the logical connection.
func (c *Conn) Close() { c.closeWith(ErrClosed) }

// IsClosed reports whether err represents normal stream closure.
func IsClosed(err error) bool {
	return errors.Is(err, ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
}

// Decode unmarshals raw JSON-RPC parameters into target.
func Decode(data []byte, target any) error { return json.Unmarshal(data, target) }

func (c *Conn) handleCall(parent context.Context, msg message) {
	key := string(msg.ID)
	ctx, cancel := context.WithCancel(context.WithValue(parent, requestIDKey{}, key))
	c.mu.Lock()
	handler := c.methods[msg.Method]
	c.active[key] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.active, key)
		c.mu.Unlock()
	}()

	if handler == nil {
		_ = c.write(context.Background(), message{JSONRPC: "2.0", ID: msg.ID, Error: &RPCError{Code: -32601, Message: "method not found"}})
		return
	}
	value, err := handler(ctx, msg.Params)
	if err != nil {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			rpcErr = &RPCError{Code: -32000, Message: err.Error()}
		}
		_ = c.write(context.Background(), message{JSONRPC: "2.0", ID: msg.ID, Error: rpcErr})
		return
	}
	result, err := marshal(value)
	if err != nil {
		_ = c.write(context.Background(), message{JSONRPC: "2.0", ID: msg.ID, Error: &RPCError{Code: -32603, Message: err.Error()}})
		return
	}
	_ = c.write(context.Background(), message{JSONRPC: "2.0", ID: msg.ID, Result: result})
}

func (c *Conn) handleNotification(ctx context.Context, msg message) {
	if msg.Method == cancelMethod {
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(msg.Params, &request) == nil {
			c.mu.Lock()
			cancel := c.active[string(request.ID)]
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		return
	}
	c.mu.Lock()
	handler := c.notices[msg.Method]
	c.mu.Unlock()
	if handler != nil {
		handler(ctx, msg.Params)
	}
}

func (c *Conn) handleResponse(msg message) {
	c.mu.Lock()
	replies := c.pending[string(msg.ID)]
	c.mu.Unlock()
	if replies == nil {
		return
	}
	if msg.Error != nil {
		replies <- response{err: msg.Error}
		return
	}
	replies <- response{result: msg.Result}
}

func (c *Conn) write(ctx context.Context, msg message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.closedError()
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.encoder.Encode(msg); err != nil {
		c.closeWith(err)
		return err
	}
	return nil
}

func (c *Conn) closeWith(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.done)
		c.mu.Lock()
		for _, cancel := range c.active {
			cancel()
		}
		c.mu.Unlock()
	})
}

func (c *Conn) closedError() error {
	if c.closeErr != nil {
		return c.closeErr
	}
	return ErrClosed
}

func marshal(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}
