package llmv1_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

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

// A throttling hint must reach a host driving a Provider Process over llm.v1
// exactly as it reaches one importing the Go package; if ErrorData drops it,
// the two integration modes disagree.
func TestErrorDataOfCarriesRetryAfter(t *testing.T) {
	err := &llm.Error{
		Kind:       llm.KindRateLimit,
		Provider:   "p",
		Status:     429,
		Message:    "slow down",
		RetryAfter: 3 * time.Second,
	}

	data := llmv1.ErrorDataOf(err)
	if data.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter = %v, want 3s", data.RetryAfter)
	}

	encoded, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	var round llmv1.ErrorData
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter after round-trip = %v, want 3s", round.RetryAfter)
	}
}
