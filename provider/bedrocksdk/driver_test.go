package bedrocksdk_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/provider/bedrocksdk"
)

func TestObserverReportsRealSDKRetry(t *testing.T) {
	var mu sync.Mutex
	var events []llm.Observation
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts++
		attempt := attempts
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			w.Header().Set("X-Amz-Retry-After", "0")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"retry"}`))
			return
		}
		_, _ = w.Write([]byte(`{
  "output":{"message":{"role":"assistant","content":[{"text":"hello"}]}},
  "stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},
  "metrics":{"latencyMs":1}}`))
	}))
	t.Cleanup(server.Close)
	client, err := bedrocksdk.New(context.Background(), bedrocksdk.Config{
		Name: "bedrock", Region: "us-east-1", BaseURL: server.URL, HTTPClient: server.Client(),
		AccessKeyID: "test", SecretAccessKey: "test",
		Observer: llm.ObserverFunc(func(_ context.Context, event llm.Observation) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(context.Background(), llm.Request{Model: "test-model"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 || len(events) != 2 || events[0].Attempt != 2 || events[1].Phase != llm.ObservationFinished {
		t.Fatalf("attempts=%d events=%#v", attempts, events)
	}
}

func TestChatTranslatesConverseRequestAndResponse(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "model/test-model/converse") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "output":{"message":{"role":"assistant","content":[{"text":"hello"}]}},
  "stopReason":"end_turn",
  "usage":{"inputTokens":3,"outputTokens":1,"totalTokens":4},
  "metrics":{"latencyMs":1}
}`))
	}))
	defer server.Close()
	client, err := bedrocksdk.New(context.Background(), bedrocksdk.Config{
		Name: "bedrock", Region: "us-east-1", BaseURL: server.URL, HTTPClient: server.Client(),
		AccessKeyID: "test", SecretAccessKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Chat(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.SystemMessage("be terse"), llm.UserMessage("hi")},
		Tools:    []llm.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "hello" || response.Usage.TotalTokens != 4 || response.FinishReason != llm.FinishStop {
		t.Fatalf("response = %#v", response)
	}
	if _, ok := request["system"]; !ok {
		t.Fatalf("system instructions missing: %#v", request)
	}
	if _, ok := request["toolConfig"]; !ok {
		t.Fatalf("tools missing: %#v", request)
	}
}
