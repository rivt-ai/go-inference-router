package rpcserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/rpcserver"
)

type source struct{}

func (source) Available(context.Context, string, config.Provider) bool { return true }
func (source) Open(context.Context, string, config.Provider) (llm.Provider, error) {
	return provider{}, nil
}

type provider struct{}

func (provider) Name() string { return "test" }
func (provider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: llm.AssistantMessage("hello")}, nil
}
func (p provider) ChatStream(ctx context.Context, req llm.Request, emit func(llm.Event) error) (*llm.Response, error) {
	if err := emit(llm.Event{Kind: llm.EventContent, Text: "hello"}); err != nil {
		return nil, err
	}
	return p.Chat(ctx, req)
}
func (provider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "raw"}}, nil
}
func (provider) Capabilities(context.Context, string) (llm.Capabilities, error) {
	return llm.Capabilities{Streaming: true, Embeddings: true}, nil
}
func (provider) ModelMetadata(context.Context, string) (llm.Metadata, error) {
	return llm.Metadata{ContextWindowTokens: 2048}, nil
}
func (provider) Embed(context.Context, llm.EmbeddingRequest) ([][]float32, error) {
	return [][]float32{{1, 2}}, nil
}

type installer struct{}

func (installer) Plan(context.Context, string, string) (llmv1.InstallPlan, error) {
	return llmv1.InstallPlan{ID: "plan", Provider: "p", Version: "1"}, nil
}
func (installer) Approve(context.Context, string) (string, error) { return "/provider", nil }
func (installer) Available(context.Context, string) (llmv1.InstallAvailableResponse, error) {
	return llmv1.InstallAvailableResponse{
		Provider: "test", InstalledVersions: []string{"v1"}, InstalledVersion: "v1",
		AvailableVersion: "v2", UpdateAvailable: true,
	}, nil
}
func (installer) Remove(context.Context, string, string) (bool, error) { return true, nil }

func TestServerExposesRouterOperations(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	server := rpcserver.New(right, right)
	initial := config.Config{
		Version:   1,
		Providers: map[string]config.Provider{"p": {Type: "test"}},
		Models:    map[string]config.ModelProfile{"friendly": {Provider: "p", Model: "raw"}},
	}
	r, err := router.New(initial, source{}, server)
	if err != nil {
		t.Fatal(err)
	}
	load := func(context.Context) (config.Config, error) {
		next := initial
		next.Models = map[string]config.ModelProfile{"friendly": {Provider: "p", Model: "raw-2"}}
		return next, nil
	}
	go func() { _ = server.Serve(context.Background(), r, installer{}, load) }()
	client := jsonrpc.New(left, left)
	observations := make(chan llmv1.ObservationNotification, 16)
	client.OnNotification(llmv1.MethodObservation, func(_ context.Context, params []byte) {
		var observation llmv1.ObservationNotification
		if jsonrpc.Decode(params, &observation) == nil {
			observations <- observation
		}
	})
	go func() { _ = client.Serve(context.Background()) }()

	var init llmv1.InitializeResponse
	if err := client.Call(context.Background(), llmv1.MethodInitialize, llmv1.InitializeRequest{
		ClientName: "test", Protocols: []string{llmv1.Protocol}, Observations: true,
	}, &init); err != nil || init.Protocol != llmv1.Protocol || !init.Observations {
		t.Fatalf("Initialize = %#v, %v", init, err)
	}
	var profiles llmv1.ProfilesResponse
	if err := client.Call(context.Background(), llmv1.MethodProfilesList, nil, &profiles); err != nil || len(profiles.Profiles) != 1 {
		t.Fatalf("Profiles = %#v, %v", profiles, err)
	}
	var discovered llmv1.ModelsDiscoverResponse
	if err := client.Call(context.Background(), llmv1.MethodModelsDiscover, llmv1.ModelsDiscoverRequest{}, &discovered); err != nil || len(discovered.Models) != 1 {
		t.Fatalf("Discover = %#v, %v", discovered, err)
	}
	var statuses llmv1.ProvidersStatusResponse
	if err := client.Call(context.Background(), llmv1.MethodProvidersStatus, nil, &statuses); err != nil || len(statuses.Providers) != 1 {
		t.Fatalf("Status = %#v, %v", statuses, err)
	}
	var capabilities llmv1.CapabilitiesResponse
	if err := client.Call(context.Background(), llmv1.MethodCapabilitiesGet, llmv1.CapabilitiesRequest{ProfileID: "friendly"}, &capabilities); err != nil || !capabilities.Capabilities.Streaming || capabilities.Metadata.ContextWindowTokens != 2048 {
		t.Fatalf("Capabilities = %#v, %v", capabilities, err)
	}
	events := make(chan llmv1.StreamEvent, 1)
	client.OnNotification(llmv1.MethodStreamEvent, func(_ context.Context, params []byte) {
		var event llmv1.StreamEvent
		_ = jsonrpc.Decode(params, &event)
		events <- event
	})
	var response llmv1.ChatResponse
	if err := client.Call(context.Background(), llmv1.MethodChat, llmv1.ChatRequest{
		ProfileID: "friendly", Stream: true, Request: llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}},
	}, &response); err != nil {
		t.Fatal(err)
	}
	if event := <-events; event.Sequence != 1 || event.Event.Text != "hello" || response.Response.Message.Content != "hello" {
		t.Fatalf("event/response = %#v / %#v", event, response)
	}
	var embedded llmv1.EmbedResponse
	if err := client.Call(context.Background(), llmv1.MethodEmbed, llmv1.EmbedRequest{
		ProfileID: "friendly", Request: llm.EmbeddingRequest{Texts: []string{"hello"}},
	}, &embedded); err != nil || len(embedded.Vectors) != 1 {
		t.Fatalf("Embed = %#v, %v", embedded, err)
	}
	var plan llmv1.InstallPlan
	if err := client.Call(context.Background(), llmv1.MethodInstallPlan, llmv1.InstallPlanRequest{Provider: "p"}, &plan); err != nil || plan.ID != "plan" {
		t.Fatalf("InstallPlan = %#v, %v", plan, err)
	}
	var installed llmv1.InstallResponse
	if err := client.Call(context.Background(), llmv1.MethodInstallApprove, llmv1.InstallApproveRequest{PlanID: plan.ID}, &installed); err != nil || installed.Path != "/provider" {
		t.Fatalf("InstallApprove = %#v, %v", installed, err)
	}
	var available llmv1.InstallAvailableResponse
	if err := client.Call(context.Background(), llmv1.MethodInstallAvailable, llmv1.InstallAvailableRequest{Provider: "test"}, &available); err != nil || !available.UpdateAvailable {
		t.Fatalf("InstallAvailable = %#v, %v", available, err)
	}
	var removed llmv1.InstallRemoveResponse
	if err := client.Call(context.Background(), llmv1.MethodInstallRemove, llmv1.InstallRemoveRequest{Provider: "test", Version: "v1"}, &removed); err != nil || !removed.Removed {
		t.Fatalf("InstallRemove = %#v, %v", removed, err)
	}
	var reloaded llmv1.ConfigReloadResponse
	if err := client.Call(context.Background(), llmv1.MethodConfigReload, nil, &reloaded); err != nil {
		t.Fatalf("ConfigReload: %v", err)
	}
	var stopped llmv1.ProviderStopResponse
	if err := client.Call(context.Background(), llmv1.MethodProviderStop, llmv1.ProviderStopRequest{ProviderID: "p"}, &stopped); err != nil {
		t.Fatalf("ProviderStop: %v", err)
	}
	select {
	case observation := <-observations:
		if observation.Operation == "" || observation.Phase == "" {
			t.Fatalf("observation = %#v", observation)
		}
	case <-time.After(time.Second):
		t.Fatal("no observation notification")
	}
}
