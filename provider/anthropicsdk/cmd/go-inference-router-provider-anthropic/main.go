package main

import (
	"context"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/protocol/providerhost"
	"github.com/rivt-ai/go-inference-router/provider/anthropicsdk"
)

var version = "dev"

func main() {
	providerhost.Main("go-inference-router-provider-anthropic", version, factory)
}

func factory(_ context.Context, request llmv1.ProviderInitializeRequest, observer llm.Observer) (llm.Provider, llm.Capabilities, error) {
	var baseURL string
	if err := providerhost.Config(request.Config, map[string]*string{"base_url": &baseURL}); err != nil {
		return nil, llm.Capabilities{}, err
	}
	client := anthropicsdk.New(anthropicsdk.Config{
		Name: request.ProviderID, APIKey: request.Secrets["api_key"], BaseURL: baseURL, Observer: observer,
	})
	return client, llm.Capabilities{
		Streaming: true, Tools: true, StructuredOutput: true, Reasoning: true,
		InputModalities: []llm.Modality{llm.ModalityText}, MaxConcurrency: 8,
	}, nil
}
