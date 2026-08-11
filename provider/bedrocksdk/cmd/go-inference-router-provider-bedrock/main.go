package main

import (
	"context"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/protocol/providerhost"
	"github.com/rivt-ai/go-inference-router/provider/bedrocksdk"
)

var version = "dev"

func main() {
	providerhost.Main("go-inference-router-provider-bedrock", version, factory)
}

func factory(ctx context.Context, request llmv1.ProviderInitializeRequest, observer llm.Observer) (llm.Provider, llm.Capabilities, error) {
	var region, profile, baseURL string
	if err := providerhost.Config(request.Config, map[string]*string{
		"region": &region, "profile": &profile, "base_url": &baseURL,
	}); err != nil {
		return nil, llm.Capabilities{}, err
	}
	client, err := bedrocksdk.New(ctx, bedrocksdk.Config{
		Name: request.ProviderID, Region: region, Profile: profile, BaseURL: baseURL,
		AccessKeyID: request.Secrets["access_key_id"], SecretAccessKey: request.Secrets["secret_access_key"],
		SessionToken: request.Secrets["session_token"], Observer: observer,
	})
	if err != nil {
		return nil, llm.Capabilities{}, err
	}
	return client, llm.Capabilities{
		Tools: true, StructuredOutput: true, Reasoning: true,
		InputModalities: []llm.Modality{llm.ModalityText, llm.ModalityImage}, MaxConcurrency: 8,
	}, nil
}
