package llmv1_test

import (
	"encoding/json"
	"errors"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func TestDomainErrorPreservesPortableFields(t *testing.T) {
	source := errors.New("source")
	err := llmv1.DomainError(&llm.Error{
		Kind: llm.KindRateLimit, Provider: "openai", Status: 429, Message: "slow down", Err: source,
	})
	if err.Code != llmv1.CodeDomainError || err.Message == "" {
		t.Fatalf("DomainError = %#v", err)
	}
	var data llmv1.ErrorData
	if decodeErr := json.Unmarshal(err.Data, &data); decodeErr != nil {
		t.Fatalf("decode data: %v", decodeErr)
	}
	if data.Kind != llm.KindRateLimit || data.Provider != "openai" || data.Status != 429 || data.Message != "slow down" {
		t.Fatalf("data = %#v", data)
	}
}
