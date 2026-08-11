package providerproc_test

import (
	"context"
	"os"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/protocol/providerhost"
	"github.com/rivt-ai/go-inference-router/router/providerproc"
)

type helperProvider struct{ observer llm.Observer }

func (helperProvider) Name() string { return "helper" }
func (p helperProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	llm.EmitObservation(ctx, p.observer, llm.Observation{
		Operation: llm.ObservationSDKRetry, Phase: llm.ObservationStarted, Attempt: 2,
	})
	return &llm.Response{Model: req.Model, Message: llm.AssistantMessage("done")}, nil
}
func (p helperProvider) ChatStream(ctx context.Context, req llm.Request, emit func(llm.Event) error) (*llm.Response, error) {
	if err := emit(llm.Event{Kind: llm.EventContent, Text: "done"}); err != nil {
		return nil, err
	}
	return p.Chat(ctx, req)
}
func (helperProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "helper-model"}}, nil
}

func TestClientNegotiatesStreamsAndStopsProviderProcess(t *testing.T) {
	observations := make(chan llm.Observation, 1)
	client, err := providerproc.Start(context.Background(), providerproc.Options{
		Path:       os.Args[0],
		Args:       []string{"-test.run=TestProviderHelperProcess"},
		Env:        append(os.Environ(), "INFROUTER_PROVIDER_HELPER=1"),
		Initialize: llmv1.ProviderInitializeRequest{ProviderID: "test", Protocols: []string{llmv1.Protocol}, Observations: true},
		Observer: llm.ObserverFunc(func(_ context.Context, observation llm.Observation) {
			observations <- observation
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.Name() != "helper" {
		t.Fatalf("name = %q", client.Name())
	}
	var text string
	response, err := client.ChatStream(context.Background(), llm.Request{Model: "helper-model"}, func(event llm.Event) error {
		text += event.Text
		return nil
	})
	if err != nil || text != "done" || response.Message.Content != "done" {
		t.Fatalf("ChatStream = %#v, text %q, err %v", response, text, err)
	}
	if observation := <-observations; observation.Operation != llm.ObservationSDKRetry || observation.Attempt != 2 {
		t.Fatalf("observation = %#v", observation)
	}
	models, err := client.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "helper-model" {
		t.Fatalf("ListModels = %#v, %v", models, err)
	}
}

func TestProviderHelperProcess(t *testing.T) {
	if os.Getenv("INFROUTER_PROVIDER_HELPER") != "1" {
		return
	}
	factory := func(_ context.Context, _ llmv1.ProviderInitializeRequest, observer llm.Observer) (llm.Provider, llm.Capabilities, error) {
		return helperProvider{observer: observer}, llm.Capabilities{
			Streaming: true, Tools: true, InputModalities: []llm.Modality{llm.ModalityText}, MaxConcurrency: 2,
		}, nil
	}
	if err := providerhost.Serve(context.Background(), os.Stdin, os.Stdout, "test", factory); err != nil && !providerhost.NormalExit(err) {
		t.Fatal(err)
	}
}
