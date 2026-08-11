package main

import (
	"context"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/protocol/providerhost"
	"github.com/rivt-ai/go-inference-router/provider/openaisdk"
)

var version = "dev"

func main() {
	providerhost.Main("go-inference-router-provider-openai", version, factory)
}

func factory(_ context.Context, request llmv1.ProviderInitializeRequest, observer llm.Observer) (llm.Provider, llm.Capabilities, error) {
	var baseURL, organization, project string
	if err := providerhost.Config(request.Config, map[string]*string{
		"base_url": &baseURL, "organization": &organization, "project": &project,
	}); err != nil {
		return nil, llm.Capabilities{}, err
	}
	client := openaisdk.New(openaisdk.Config{
		Name: request.ProviderID, APIKey: request.Secrets["api_key"], BaseURL: baseURL,
		Organization: organization, Project: project, Observer: observer,
	})
	return client, llm.Capabilities{
		Streaming: true, Tools: true, StructuredOutput: true, Reasoning: true, Embeddings: true,
		InputModalities: []llm.Modality{llm.ModalityText}, MaxConcurrency: 8,
	}, nil
}
