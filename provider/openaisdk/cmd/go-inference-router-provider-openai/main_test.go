package main

import (
	"context"
	"encoding/json"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func TestFactoryDeclaresOpenAICapabilities(t *testing.T) {
	provider, capabilities, err := factory(context.Background(), llmv1.ProviderInitializeRequest{
		ProviderID: "openai", Config: map[string]json.RawMessage{"base_url": json.RawMessage(`"https://example.test/v1"`)},
		Secrets: map[string]string{"api_key": "secret"},
	}, nil)
	if err != nil || provider.Name() != "openai" || !capabilities.Streaming || !capabilities.Embeddings || !capabilities.Supports(llm.ModalityText) {
		t.Fatalf("factory = %v, %#v, %v", provider, capabilities, err)
	}
}
