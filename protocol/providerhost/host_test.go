package providerhost

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

type testProvider struct{ closed bool }

func (p *testProvider) Name() string { return "test-provider" }
func (p *testProvider) Chat(_ context.Context, request llm.Request) (*llm.Response, error) {
	return &llm.Response{Model: request.Model, Message: llm.AssistantMessage("done")}, nil
}
func (p *testProvider) ChatStream(ctx context.Context, request llm.Request, emit func(llm.Event) error) (*llm.Response, error) {
	if err := emit(llm.Event{Kind: llm.EventContent, Text: "done"}); err != nil {
		return nil, err
	}
	return p.Chat(ctx, request)
}
func (*testProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model"}}, nil
}
func (*testProvider) ModelMetadata(context.Context, string) (llm.Metadata, error) {
	return llm.Metadata{ContextWindowTokens: 1024}, nil
}
func (*testProvider) Embed(context.Context, llm.EmbeddingRequest) ([][]float32, error) {
	return [][]float32{{1, 2}}, nil
}
func (p *testProvider) Close() error { p.closed = true; return nil }

func TestServeExposesProviderOperations(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	provider := &testProvider{}
	factory := func(context.Context, llmv1.ProviderInitializeRequest, llm.Observer) (llm.Provider, llm.Capabilities, error) {
		return provider, llm.Capabilities{Streaming: true, Embeddings: true}, nil
	}
	go func() { _ = Serve(context.Background(), right, right, "1.2.3", factory) }()
	client := jsonrpc.New(left, left)
	stream := make(chan llmv1.StreamEvent, 1)
	client.OnNotification(llmv1.MethodStreamEvent, func(_ context.Context, params []byte) {
		var event llmv1.StreamEvent
		if err := jsonrpc.Decode(params, &event); err == nil {
			stream <- event
		}
	})
	go func() { _ = client.Serve(context.Background()) }()

	var initialized llmv1.ProviderInitializeResponse
	err := client.Call(context.Background(), llmv1.MethodProviderInitialize, llmv1.ProviderInitializeRequest{
		ProviderID: "test", Protocols: []string{llmv1.Protocol},
	}, &initialized)
	if err != nil || initialized.Name != provider.Name() || initialized.Version != "1.2.3" || !initialized.Capabilities.Streaming {
		t.Fatalf("initialize = %#v, %v", initialized, err)
	}

	var chat llmv1.ChatResponse
	err = client.Call(context.Background(), llmv1.MethodProviderChat, llmv1.ProviderChatRequest{
		StreamID: "stream-1", Request: llm.Request{Model: "model"},
	}, &chat)
	if err != nil || chat.Response.Message.Content != "done" {
		t.Fatalf("chat = %#v, %v", chat, err)
	}
	if event := <-stream; event.RequestID != "stream-1" || event.Sequence != 1 || event.Event.Text != "done" {
		t.Fatalf("stream event = %#v", event)
	}

	var models llmv1.ProviderModelsResponse
	if err := client.Call(context.Background(), llmv1.MethodProviderModels, nil, &models); err != nil || len(models.Models) != 1 {
		t.Fatalf("models = %#v, %v", models, err)
	}
	var metadata llmv1.ProviderMetadataResponse
	if err := client.Call(context.Background(), llmv1.MethodProviderMetadata, llmv1.ProviderMetadataRequest{Model: "model"}, &metadata); err != nil || metadata.Metadata.ContextWindowTokens != 1024 {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}
	var embedded llmv1.EmbedResponse
	if err := client.Call(context.Background(), llmv1.MethodProviderEmbed, llmv1.ProviderEmbedRequest{Request: llm.EmbeddingRequest{Texts: []string{"x"}}}, &embedded); err != nil || len(embedded.Vectors) != 1 {
		t.Fatalf("embed = %#v, %v", embedded, err)
	}
	if err := client.Call(context.Background(), llmv1.MethodShutdown, nil, nil); err != nil || !provider.closed {
		t.Fatalf("shutdown: closed=%v, err=%v", provider.closed, err)
	}
}

func TestConfigAndRun(t *testing.T) {
	values := map[string]json.RawMessage{"base_url": json.RawMessage(`"https://example.test"`)}
	var baseURL string
	if err := Config(values, map[string]*string{"base_url": &baseURL, "missing": nil}); err != nil || baseURL != "https://example.test" {
		t.Fatalf("Config = %q, %v", baseURL, err)
	}
	values["base_url"] = json.RawMessage(`false`)
	if err := Config(values, map[string]*string{"base_url": &baseURL}); err == nil {
		t.Fatal("Config accepted a non-string value")
	}
	if err := run(context.Background(), bytes.NewBufferString(""), &bytes.Buffer{}, "dev", nil); err != nil {
		t.Fatalf("run EOF = %v", err)
	}
}
