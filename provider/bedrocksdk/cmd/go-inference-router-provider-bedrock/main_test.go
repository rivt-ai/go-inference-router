package main

import (
	"context"
	"encoding/json"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func TestFactoryDeclaresBedrockCapabilities(t *testing.T) {
	provider, capabilities, err := factory(context.Background(), llmv1.ProviderInitializeRequest{
		ProviderID: "bedrock",
		Config: map[string]json.RawMessage{
			"region": json.RawMessage(`"us-east-1"`), "base_url": json.RawMessage(`"https://example.test"`),
		},
		Secrets: map[string]string{"access_key_id": "id", "secret_access_key": "secret"},
	}, nil)
	if err != nil || provider.Name() != "bedrock" || capabilities.Streaming || !capabilities.Tools || !capabilities.Supports(llm.ModalityImage) {
		t.Fatalf("factory = %v, %#v, %v", provider, capabilities, err)
	}
}
