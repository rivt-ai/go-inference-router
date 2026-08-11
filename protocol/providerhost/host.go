// Package providerhost exposes an in-process llm.Provider over llm.v1 JSON-RPC.
package providerhost

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

// Factory builds a provider and declares its process capabilities.
type Factory func(context.Context, llmv1.ProviderInitializeRequest, llm.Observer) (llm.Provider, llm.Capabilities, error)

type host struct {
	conn    *jsonrpc.Conn
	version string
	factory Factory

	mu       sync.RWMutex
	provider llm.Provider
}

type correlationKey struct{}

// Serve exposes a lazily initialized provider over llm.v1 JSON-RPC.
func Serve(ctx context.Context, reader io.Reader, writer io.Writer, version string, factory Factory) error {
	h := &host{conn: jsonrpc.New(reader, writer), version: version, factory: factory}
	h.register()
	return h.conn.Serve(ctx)
}

func (h *host) register() {
	h.conn.Handle(llmv1.MethodProviderInitialize, h.initialize)
	h.conn.Handle(llmv1.MethodProviderChat, h.chat)
	h.conn.Handle(llmv1.MethodProviderModels, h.models)
	h.conn.Handle(llmv1.MethodProviderMetadata, h.metadata)
	h.conn.Handle(llmv1.MethodProviderEmbed, h.embed)
	h.conn.Handle(llmv1.MethodShutdown, h.shutdown)
}

func (h *host) initialize(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderInitializeRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	if !slices.Contains(request.Protocols, llmv1.Protocol) {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindProtocol, Provider: request.ProviderID, Message: "no supported protocol version",
		})
	}
	var observer llm.Observer
	if request.Observations {
		observer = h
	}
	created, capabilities, err := h.factory(ctx, request, observer)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	h.mu.Lock()
	h.provider = created
	h.mu.Unlock()
	return llmv1.ProviderInitializeResponse{
		Protocol: llmv1.Protocol, Name: created.Name(), Version: h.version,
		Capabilities: capabilities, Observations: request.Observations,
	}, nil
}

func (h *host) chat(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderChatRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	ctx = context.WithValue(ctx, correlationKey{}, request.CorrelationID)
	current, err := h.current()
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	var response *llm.Response
	if request.StreamID == "" {
		response, err = current.Chat(ctx, request.Request)
	} else if streamer, ok := current.(llm.Streamer); ok {
		var sequence uint64
		response, err = streamer.ChatStream(ctx, request.Request, func(event llm.Event) error {
			sequence++
			return h.conn.Notify(ctx, llmv1.MethodStreamEvent, llmv1.StreamEvent{
				RequestID: request.StreamID, Sequence: sequence, Event: event,
			})
		})
	} else {
		err = &llm.Error{Kind: llm.KindInvalidRequest, Provider: current.Name(), Message: "streaming is unsupported"}
	}
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ChatResponse{Response: *response}, nil
}

func (h *host) models(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderModelsRequest
	if len(params) != 0 {
		if err := jsonrpc.Decode(params, &request); err != nil {
			return nil, jsonrpc.InvalidParams(err)
		}
	}
	ctx = context.WithValue(ctx, correlationKey{}, request.CorrelationID)
	current, err := h.current()
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	lister, ok := current.(llm.ModelLister)
	if !ok {
		return llmv1.ProviderModelsResponse{}, nil
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ProviderModelsResponse{Models: models}, nil
}

func (h *host) metadata(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderMetadataRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	ctx = context.WithValue(ctx, correlationKey{}, request.CorrelationID)
	current, err := h.current()
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	reporter, ok := current.(llm.MetadataReporter)
	if !ok {
		return llmv1.ProviderMetadataResponse{}, nil
	}
	metadata, err := reporter.ModelMetadata(ctx, request.Model)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.ProviderMetadataResponse{Metadata: metadata}, nil
}

func (h *host) embed(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderEmbedRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	ctx = context.WithValue(ctx, correlationKey{}, request.CorrelationID)
	current, err := h.current()
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	embedder, ok := current.(llm.Embedder)
	if !ok {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindInvalidRequest, Provider: current.Name(), Message: "embeddings are unsupported",
		})
	}
	vectors, err := embedder.Embed(ctx, request.Request)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.EmbedResponse{Vectors: vectors}, nil
}

func (h *host) shutdown(context.Context, []byte) (any, error) {
	current, err := h.current()
	if err == nil {
		if closer, ok := current.(io.Closer); ok {
			err = closer.Close()
		}
	}
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return struct{}{}, nil
}

func (h *host) current() (llm.Provider, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.provider == nil {
		return nil, &llm.Error{
			Kind: llm.KindProtocol, Provider: "provider-process", Message: "provider is not initialized",
		}
	}
	return h.provider, nil
}

// Observe forwards Provider Process observations to the Router.
func (h *host) Observe(ctx context.Context, event llm.Observation) {
	if requestID, _ := ctx.Value(correlationKey{}).(string); event.RequestID == "" {
		event.RequestID = requestID
	}
	_ = h.conn.Notify(ctx, llmv1.MethodObservation, llmv1.ObservationNotificationOf(event))
}

// NormalExit reports whether a provider host stopped through normal shutdown.
func NormalExit(err error) bool { return jsonrpc.IsClosed(err) || errors.Is(err, context.Canceled) }
