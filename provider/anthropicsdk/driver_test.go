package anthropicsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rivt-ai/go-inference-router"
)

// newTestClient points the SDK at a stub server. Retries are disabled so an
// error-classification test asserts on one response rather than waiting out
// the SDK's backoff.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(Config{
		Name:         "test",
		BaseURL:      server.URL,
		APIKey:       "k",
		MaxRetries:   -1,
		StallTimeout: time.Minute,
	})
}

func captureBody(t *testing.T, req inference.Request, reply string) map[string]any {
	t.Helper()
	var body map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(w, 0, reply)
	})
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return body
}

// writeJSON sets the content type required by the SDK response decoder.
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
	}
	_, _ = io.WriteString(w, body)
}

const okReply = `{"id":"msg_1","type":"message","role":"assistant","model":"m",
	"stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],
	"usage":{"input_tokens":1,"output_tokens":2}}`

func TestObserverReportsRealSDKRetry(t *testing.T) {
	var mu sync.Mutex
	var events []inference.Observation
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts++
		attempt := attempts
		mu.Unlock()
		if attempt == 1 {
			w.Header().Set("Retry-After-Ms", "0")
			writeJSON(w, http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"retry"}}`)
			return
		}
		writeJSON(w, 0, okReply)
	}))
	t.Cleanup(server.Close)
	client := New(Config{
		Name: "test", BaseURL: server.URL, APIKey: "k", MaxRetries: 1,
		Observer: inference.ObserverFunc(func(_ context.Context, event inference.Observation) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}),
	})
	if _, err := client.Chat(context.Background(), inference.Request{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 || len(events) != 2 || events[0].Attempt != 2 || events[1].Phase != inference.ObservationFinished {
		t.Fatalf("attempts=%d events=%#v", attempts, events)
	}
}

func TestSendsRequiredHeaders(t *testing.T) {
	var got http.Header
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		writeJSON(w, 0, okReply)
	})
	if _, err := client.Chat(context.Background(), inference.Request{Model: "m"}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.Get("x-api-key") != "k" {
		t.Errorf("x-api-key = %q, want k", got.Get("x-api-key"))
	}
	if got.Get("anthropic-version") == "" {
		t.Error("SDK did not send anthropic-version")
	}
}

// TestSystemMessagesAreHoisted verifies Anthropic's top-level system shape.
func TestSystemMessagesAreHoisted(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model: "m",
		Messages: []inference.Message{
			inference.SystemMessage("first"),
			inference.UserMessage("hello"),
			inference.SystemMessage("second"),
		},
	}, okReply)

	system, _ := body["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %v, want one text block", body["system"])
	}
	if text := system[0].(map[string]any)["text"]; text != "first\n\nsecond" {
		t.Errorf("system text = %q, want both system messages joined", text)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want only the user turn: %v", len(messages), messages)
	}
}

func TestToolResultsCoalesceIntoOneUserMessage(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model: "m",
		Messages: []inference.Message{
			inference.UserMessage("weather in both cities?"),
			inference.AssistantMessage("",
				inference.ToolCall{ID: "t1", Name: "w", Arguments: `{"city":"Oslo"}`},
				inference.ToolCall{ID: "t2", Name: "w", Arguments: `{"city":"Rome"}`},
			),
			inference.ToolMessage("t1", "cold"),
			inference.ToolMessage("t2", "warm"),
			inference.UserMessage("thanks"),
		},
	}, okReply)

	messages, _ := body["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("got %d messages, want user/assistant/tool-results/user: %v", len(messages), messages)
	}

	assistant, _ := messages[1].(map[string]any)
	blocks, _ := assistant["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("assistant blocks = %v, want two tool_use blocks and no empty text block", blocks)
	}
	first, _ := blocks[0].(map[string]any)
	if input, ok := first["input"].(map[string]any); !ok || input["city"] != "Oslo" {
		t.Errorf("tool input = %v, want a decoded object", first["input"])
	}

	results, _ := messages[2].(map[string]any)
	if results["role"] != "user" {
		t.Errorf("tool results landed on role %v, want user", results["role"])
	}
	if resultBlocks, _ := results["content"].([]any); len(resultBlocks) != 2 {
		t.Fatalf("got %d tool_result blocks in one message, want 2", len(resultBlocks))
	}
}

func TestMaxTokensIsAlwaysSent(t *testing.T) {
	body := captureBody(t, inference.Request{Model: "m"}, okReply)
	if body["max_tokens"] != float64(DefaultMaxTokens) {
		t.Errorf("max_tokens = %v, want the driver default %d", body["max_tokens"], DefaultMaxTokens)
	}
	body = captureBody(t, inference.Request{Model: "m", MaxOutputTokens: 64}, okReply)
	if body["max_tokens"] != float64(64) {
		t.Errorf("max_tokens = %v, want 64", body["max_tokens"])
	}
}

func TestToolChoiceRequiredMapsToAny(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model:      "m",
		ToolChoice: inference.ToolChoiceRequired,
		Tools: []inference.Tool{{
			Name:        "w",
			Description: "weather",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
				"required":   []string{"city"},
			},
		}},
	}, okReply)

	choice, _ := body["tool_choice"].(map[string]any)
	if choice["type"] != "any" {
		t.Errorf("tool_choice = %v, want type any", body["tool_choice"])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	schema, _ := tool["input_schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("input_schema = %v, want an object schema", tool["input_schema"])
	}
	if _, ok := schema["properties"]; !ok {
		t.Errorf("input_schema lost the properties: %v", schema)
	}
}

func TestUsageSumsPromptTokens(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 0, `{"id":"m1","type":"message","role":"assistant","model":"m",
			"stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],
			"usage":{"input_tokens":10,"output_tokens":5,
			"cache_read_input_tokens":90,"cache_creation_input_tokens":100}}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.PromptTokens != 200 || resp.Usage.CachedPromptTokens != 90 || resp.Usage.TotalTokens != 205 {
		t.Errorf("usage = %+v, want prompt 200 / cached 90 / total 205", resp.Usage)
	}
}

func TestDecodesTextThinkingAndToolCalls(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 0, `{"id":"m1","type":"message","role":"assistant","model":"m",
			"stop_reason":"tool_use","content":[
			{"type":"thinking","thinking":"pondering","signature":"s"},
			{"type":"text","text":"checking"},
			{"type":"tool_use","id":"t1","name":"w","input":{"city":"Oslo"}}],
			"usage":{"input_tokens":1,"output_tokens":2}}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Message.Reasoning != "pondering" || resp.Message.Content != "checking" {
		t.Errorf("content = %q reasoning = %q", resp.Message.Content, resp.Message.Reasoning)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(resp.Message.ToolCalls[0].Arguments), &args); err != nil || args["city"] != "Oslo" {
		t.Errorf("arguments = %q, want the object as a JSON string", resp.Message.ToolCalls[0].Arguments)
	}
	if resp.FinishReason != inference.FinishToolCalls {
		t.Errorf("finish = %q", resp.FinishReason)
	}
}

func TestRefusalIsNotAProtocolError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 0, `{"id":"m1","type":"message","role":"assistant","model":"m",
			"stop_reason":"refusal","content":[],"usage":{"input_tokens":1,"output_tokens":0}}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if err != nil {
		t.Fatalf("a refusal must be a response, not an error: %v", err)
	}
	if resp.FinishReason != inference.FinishFilter {
		t.Errorf("finish = %q, want the filter reason", resp.FinishReason)
	}
}

func writeFrames(w http.ResponseWriter, frames ...[2]string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, frame := range frames {
		_, _ = io.WriteString(w, "event: "+frame[0]+"\ndata: "+frame[1]+"\n\n")
	}
}

func TestChatStreamAccumulatesTextAndToolCalls(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream flag = %v, want true", body["stream"])
		}
		writeFrames(w,
			[2]string{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"usage":{"input_tokens":7,"cache_read_input_tokens":3,"output_tokens":0}}}`},
			[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`},
			[2]string{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			[2]string{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"w","input":{}}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Oslo\"}"}}`},
			[2]string{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			[2]string{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":11}}`},
			[2]string{"message_stop", `{"type":"message_stop"}`},
		)
	})

	var text strings.Builder
	var calls []inference.ToolCall
	resp, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, func(e inference.Event) error {
		switch e.Kind {
		case inference.EventContent:
			text.WriteString(e.Text)
		case inference.EventToolCall:
			calls = append(calls, e.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if text.String() != "Hello" || resp.Message.Content != "Hello" {
		t.Errorf("content = %q / streamed %q", resp.Message.Content, text.String())
	}
	if len(calls) != 1 || calls[0].ID != "t1" || calls[0].Arguments != `{"city":"Oslo"}` {
		t.Fatalf("tool call events = %+v", calls)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "w" {
		t.Errorf("accumulated tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.FinishReason != inference.FinishToolCalls {
		t.Errorf("finish = %q", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 11 {
		t.Errorf("usage = %+v, want prompt 10 / completion 11", resp.Usage)
	}
}

func TestStreamHandlerErrorAbortsStream(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeFrames(w,
			[2]string{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`},
			[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"b"}}`},
		)
	})
	want := io.ErrClosedPipe
	seen := 0
	_, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, func(inference.Event) error {
		seen++
		return want
	})
	if err != want {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if seen != 1 {
		t.Errorf("handler called %d times, want 1", seen)
	}
}

func TestDriverImplementsOnlyTheCapabilitiesItHas(t *testing.T) {
	var provider inference.Provider = New(Config{})

	if _, ok := provider.(inference.Streamer); !ok {
		t.Error("driver should implement Streamer")
	}
	if _, ok := provider.(inference.ModelLister); !ok {
		t.Error("driver should implement ModelLister")
	}
	if _, ok := provider.(inference.MetadataReporter); !ok {
		t.Error("driver should implement MetadataReporter")
	}
	if _, ok := provider.(inference.Embedder); ok {
		t.Error("driver must not implement Embedder — the provider has no embeddings endpoint")
	}
}

func TestUnconstrainedJSONIsRefusedNotDowngraded(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("a request the driver cannot express must not reach the provider")
	})
	_, err := client.Chat(context.Background(), inference.Request{
		Model:          "m",
		ResponseFormat: inference.FormatJSON,
	})
	if !inference.IsKind(err, inference.KindInvalidRequest) {
		t.Fatalf("kind = %q, want invalid_request (err: %v)", inference.KindOf(err), err)
	}
}

func TestSchemaFormatIsEncoded(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model:          "m",
		ResponseFormat: inference.FormatSchema,
		ResponseSchema: map[string]any{"type": "object"},
	}, okReply)

	output, _ := body["output_config"].(map[string]any)
	format, _ := output["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("output_config = %v, want a json_schema format", body["output_config"])
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Errorf("schema = %v, want the caller's schema", format["schema"])
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		want   inference.Kind
	}{
		{401, inference.KindAuth},
		{403, inference.KindAuth},
		{429, inference.KindRateLimit},
		{400, inference.KindInvalidRequest},
		{413, inference.KindInvalidRequest},
		{529, inference.KindUnavailable},
		{502, inference.KindUnavailable},
	}
	for _, tc := range cases {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, tc.status, `{"type":"error","error":{"type":"x","message":"boom"}}`)
		})
		_, err := client.Chat(context.Background(), inference.Request{Model: "m"})
		if got := inference.KindOf(err); got != tc.want {
			t.Errorf("status %d: kind = %q, want %q (err: %v)", tc.status, got, tc.want, err)
		}
		var infErr *inference.Error
		if !errors.As(err, &infErr) || infErr.Status != tc.status {
			t.Errorf("status %d: error did not carry the status: %v", tc.status, err)
		}
	}
}

func TestModelMetadataReadsContextWindow(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/models/claude-opus-5") {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(w, 0, `{"id":"claude-opus-5","type":"model","display_name":"Claude Opus 5",
			"created_at":"2026-01-01T00:00:00Z","max_input_tokens":1000000,"max_tokens":128000}`)
	})
	meta, err := client.ModelMetadata(context.Background(), "claude-opus-5")
	if err != nil {
		t.Fatalf("ModelMetadata: %v", err)
	}
	if meta.ContextWindowTokens != 1_000_000 {
		t.Errorf("context window = %d, want 1000000", meta.ContextWindowTokens)
	}
}

func TestUnknownModelMetadataIsAbsentNotAnError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, `{"type":"error","error":{"type":"not_found_error","message":"nope"}}`)
	})
	meta, err := client.ModelMetadata(context.Background(), "nope")
	if err != nil || meta != (inference.Metadata{}) {
		t.Fatalf("meta = %+v err = %v, want zero and nil", meta, err)
	}
}

// TestListModelsFollowsPagination exercises the SDK's auto-pager, which is one
// of the concrete things this driver gets for free.
func TestListModelsFollowsPagination(t *testing.T) {
	calls := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(w, 0, `{"data":[{"id":"a","type":"model","display_name":"A","created_at":"2026-01-01T00:00:00Z"}],"has_more":true,"last_id":"a"}`)
			return
		}
		if after := r.URL.Query().Get("after_id"); after != "a" {
			t.Errorf("second page after_id = %q, want a", after)
		}
		writeJSON(w, 0, `{"data":[{"id":"b","type":"model","display_name":"B","created_at":"2026-01-01T00:00:00Z"}],"has_more":false}`)
	})

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[1].ID != "b" {
		t.Fatalf("models = %+v, want both pages", models)
	}
}

func TestCancellationIsClassified(t *testing.T) {
	client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Chat(ctx, inference.Request{Model: "m"})
	if !inference.IsKind(err, inference.KindCanceled) {
		t.Fatalf("kind = %q, want canceled (err: %v)", inference.KindOf(err), err)
	}
}
