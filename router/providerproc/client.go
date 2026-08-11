// Package providerproc starts and speaks to one llm.v1 Provider Process.
package providerproc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

// Options controls Provider Process startup and negotiation.
type Options struct {
	Path       string
	Args       []string
	Env        []string
	Stderr     io.Writer
	Initialize llmv1.ProviderInitializeRequest
	Timeout    time.Duration
	Observer   llm.Observer
}

type stream struct {
	handler  func(llm.Event) error
	cancel   context.CancelFunc
	sequence uint64
	err      error
}

// Client implements llm provider interfaces over a child process.
type Client struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	conn      *jsonrpc.Conn
	info      llmv1.ProviderInitializeResponse
	slots     chan struct{}
	done      chan struct{}
	waitErr   error
	streamsMu sync.Mutex
	streams   map[string]*stream
	closeOnce sync.Once
	closeErr  error
	observer  llm.Observer
}

// Start launches and initializes one Provider Process.
func Start(ctx context.Context, options Options) (*Client, error) {
	if options.Path == "" {
		return nil, errors.New("provider executable path is empty")
	}
	cmd := exec.Command(options.Path, options.Args...)
	cmd.Env = options.Env
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	cmd.Stderr = options.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client := &Client{
		cmd: cmd, stdin: stdin, conn: jsonrpc.New(stdout, stdin), done: make(chan struct{}), streams: map[string]*stream{},
		observer: options.Observer,
	}
	client.conn.OnNotification(llmv1.MethodStreamEvent, client.handleEvent)
	client.conn.OnNotification(llmv1.MethodObservation, client.handleObservation)
	go func() {
		serveErr := client.conn.Serve(context.Background())
		waitErr := cmd.Wait()
		if jsonrpc.IsClosed(serveErr) {
			serveErr = nil
		}
		client.waitErr = errors.Join(serveErr, waitErr)
		close(client.done)
	}()
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.call(initCtx, llmv1.MethodProviderInitialize, options.Initialize, &client.info); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		<-client.done
		return nil, fmt.Errorf("initialize provider process: %w", err)
	}
	max := client.info.Capabilities.MaxConcurrency
	if max < 1 {
		max = 1
	}
	client.slots = make(chan struct{}, max)
	return client, nil
}

// Name implements llm.Provider.
func (c *Client) Name() string { return c.info.Name }

// Capabilities implements llm.CapabilityReporter.
func (c *Client) Capabilities(context.Context, string) (llm.Capabilities, error) {
	return c.info.Capabilities, nil
}

// Chat implements llm.Provider.
func (c *Client) Chat(ctx context.Context, request llm.Request) (*llm.Response, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	var response llmv1.ChatResponse
	if err := c.call(ctx, llmv1.MethodProviderChat, llmv1.ProviderChatRequest{
		CorrelationID: jsonrpc.RequestID(ctx), Request: request,
	}, &response); err != nil {
		return nil, err
	}
	return &response.Response, nil
}

// ChatStream implements llm.Streamer.
func (c *Client) ChatStream(ctx context.Context, request llm.Request, handler func(llm.Event) error) (*llm.Response, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	id, err := streamID()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithCancel(ctx)
	state := &stream{handler: handler, cancel: cancel}
	c.streamsMu.Lock()
	c.streams[id] = state
	c.streamsMu.Unlock()
	defer func() {
		cancel()
		c.streamsMu.Lock()
		delete(c.streams, id)
		c.streamsMu.Unlock()
	}()
	var response llmv1.ChatResponse
	err = c.call(callCtx, llmv1.MethodProviderChat, llmv1.ProviderChatRequest{
		StreamID: id, CorrelationID: jsonrpc.RequestID(ctx), Request: request,
	}, &response)
	c.streamsMu.Lock()
	handlerErr := state.err
	c.streamsMu.Unlock()
	if handlerErr != nil {
		return nil, handlerErr
	}
	if err != nil {
		return nil, err
	}
	return &response.Response, nil
}

// ListModels implements llm.ModelLister.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	var response llmv1.ProviderModelsResponse
	if err := c.call(ctx, llmv1.MethodProviderModels, llmv1.ProviderModelsRequest{
		CorrelationID: jsonrpc.RequestID(ctx),
	}, &response); err != nil {
		return nil, err
	}
	return response.Models, nil
}

// ModelMetadata implements llm.MetadataReporter.
func (c *Client) ModelMetadata(ctx context.Context, model string) (llm.Metadata, error) {
	var response llmv1.ProviderMetadataResponse
	if err := c.call(ctx, llmv1.MethodProviderMetadata, llmv1.ProviderMetadataRequest{
		Model: model, CorrelationID: jsonrpc.RequestID(ctx),
	}, &response); err != nil {
		return llm.Metadata{}, err
	}
	return response.Metadata, nil
}

// Embed implements llm.Embedder.
func (c *Client) Embed(ctx context.Context, request llm.EmbeddingRequest) ([][]float32, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	var response llmv1.EmbedResponse
	if err := c.call(ctx, llmv1.MethodProviderEmbed, llmv1.ProviderEmbedRequest{
		CorrelationID: jsonrpc.RequestID(ctx), Request: request,
	}, &response); err != nil {
		return nil, err
	}
	return response.Vectors, nil
}

// Close gracefully stops the Provider Process and kills it after a timeout.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.call(ctx, llmv1.MethodShutdown, nil, nil)
		_ = c.stdin.Close()
		select {
		case <-c.done:
			c.closeErr = c.waitErr
		case <-ctx.Done():
			_ = c.cmd.Process.Kill()
			<-c.done
			c.closeErr = c.waitErr
		}
		c.conn.Close()
	})
	return c.closeErr
}

func (c *Client) handleEvent(_ context.Context, params []byte) {
	var event llmv1.StreamEvent
	if json.Unmarshal(params, &event) != nil {
		return
	}
	c.streamsMu.Lock()
	defer c.streamsMu.Unlock()
	state := c.streams[event.RequestID]
	if state == nil || state.err != nil {
		return
	}
	if event.Sequence != state.sequence+1 {
		state.err = &llm.Error{Kind: llm.KindProtocol, Provider: c.Name(), Message: "out-of-order stream event"}
		state.cancel()
		return
	}
	state.sequence = event.Sequence
	if state.handler != nil {
		if err := state.handler(event.Event); err != nil {
			state.err = err
			state.cancel()
		}
	}
}

func (c *Client) handleObservation(ctx context.Context, params []byte) {
	var notification llmv1.ObservationNotification
	if json.Unmarshal(params, &notification) != nil {
		return
	}
	llm.EmitObservation(ctx, c.observer, notification.Observation())
}

func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return &llm.Error{Kind: llm.KindUnavailable, Provider: c.Name(), Message: "provider process exited"}
	}
}

func (c *Client) release() { <-c.slots }

func (c *Client) call(ctx context.Context, method string, request, response any) error {
	err := c.conn.Call(ctx, method, request, response)
	var rpcErr *jsonrpc.RPCError
	if !errors.As(err, &rpcErr) {
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &llm.Error{Kind: llm.KindUnavailable, Provider: c.Name(), Message: err.Error(), Err: err}
	}
	var data llmv1.ErrorData
	if len(rpcErr.Data) != 0 && json.Unmarshal(rpcErr.Data, &data) == nil {
		return &llm.Error{Kind: data.Kind, Provider: data.Provider, Status: data.Status, Message: data.Message, Err: rpcErr}
	}
	return &llm.Error{Kind: llm.KindProtocol, Provider: c.Name(), Message: rpcErr.Message, Err: rpcErr}
}

func streamID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
