package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rivt-ai/go-inference-router"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(Config{Name: "test", BaseURL: server.URL, APIKey: "k", StallTimeout: time.Minute})
}

func TestChatDecodesMessageAndUsage(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth header = %q, want Bearer k", got)
		}
		_, _ = io.WriteString(w, `{"id":"c1","model":"m","choices":[{"finish_reason":"stop",
			"message":{"content":"hi","tool_calls":[{"id":"t1","function":{"name":"ls","arguments":"{}"}}]}}],
			"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)
	})

	resp, err := client.Chat(context.Background(), inference.Request{
		Model:    "m",
		Messages: []inference.Message{inference.UserMessage("yo")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
	if resp.FinishReason != inference.FinishStop {
		t.Errorf("finish = %q, want stop", resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "ls" {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("total tokens = %d, want 7", resp.Usage.TotalTokens)
	}
}

func TestChatEncodesOptionalFieldsOnlyWhenSet(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})

	if _, err := client.Chat(context.Background(), inference.Request{
		Model:    "m",
		Messages: []inference.Message{inference.UserMessage("yo")},
		Extra:    map[string]any{"cache_prompt": true, "model": "ignored"},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for _, absent := range []string{"temperature", "top_p", "max_completion_tokens", "seed", "parallel_tool_calls", "response_format", "tools"} {
		if _, ok := body[absent]; ok {
			t.Errorf("unset field %q was sent: %v", absent, body[absent])
		}
	}
	if body["cache_prompt"] != true {
		t.Errorf("extra field not merged: %v", body)
	}
	if body["model"] != "m" {
		t.Errorf("extra overrode declared field: model = %v, want m", body["model"])
	}
}

func TestChatEncodesSetOptions(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})

	if _, err := client.Chat(context.Background(), inference.Request{
		Model:           "m",
		Messages:        []inference.Message{inference.UserMessage("yo")},
		Temperature:     inference.Float(0.2),
		MaxOutputTokens: 64,
		ResponseFormat:  inference.FormatJSON,
		Tools:           []inference.Tool{{Name: "ls", Parameters: map[string]any{"type": "object"}}},
		ToolChoice:      inference.ToolChoiceRequired,
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if body["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want 0.2", body["temperature"])
	}
	if body["max_completion_tokens"] != float64(64) {
		t.Errorf("max_completion_tokens = %v, want 64", body["max_completion_tokens"])
	}
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want required", body["tool_choice"])
	}
	format, _ := body["response_format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Errorf("response_format = %v", body["response_format"])
	}
	if tools, _ := body["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools = %v, want 1 entry", body["tools"])
	}
}

// TestParallelToolCallsIsEncodedBothWays pins the tri-state: the provider
// defaults to true, so a caller asking for false must see that false on the
// wire rather than have it collapse into the unset case.
func TestParallelToolCallsIsEncodedBothWays(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
	}{
		{name: "enabled", set: true},
		{name: "disabled", set: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			})

			if _, err := client.Chat(context.Background(), inference.Request{
				Model:             "m",
				Messages:          []inference.Message{inference.UserMessage("yo")},
				ParallelToolCalls: inference.Bool(tc.set),
			}); err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if body["parallel_tool_calls"] != tc.set {
				t.Errorf("parallel_tool_calls = %v, want %v", body["parallel_tool_calls"], tc.set)
			}
		})
	}
}

// TestSchemaFormatIsEncoded pins the other half of the seam's structured-output
// contract: this provider supports both the unconstrained and the
// schema-constrained mode, so neither is refused here.
func TestSchemaFormatIsEncoded(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})

	if _, err := client.Chat(context.Background(), inference.Request{
		Model:          "m",
		ResponseFormat: inference.FormatSchema,
		ResponseSchema: map[string]any{"type": "object"},
		SchemaName:     "palette",
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	format, _ := body["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("response_format = %v, want json_schema", body["response_format"])
	}
	schema, _ := format["json_schema"].(map[string]any)
	if schema["name"] != "palette" || schema["strict"] != true {
		t.Errorf("json_schema = %v, want the caller's name and strict mode", format["json_schema"])
	}
}

func TestChatEmptyChoicesIsEmptyResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	})
	_, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if !inference.IsKind(err, inference.KindEmptyResponse) {
		t.Fatalf("kind = %q, want empty_response (err: %v)", inference.KindOf(err), err)
	}
}

func TestBaseURLWithV1SuffixIsNotDoubled(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL + "/v1/"})
	if _, err := client.Chat(context.Background(), inference.Request{Model: "m"}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", path)
	}
}

func TestChatStreamAccumulatesTextAndToolCalls(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream flag = %v, want true", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"id":"c1","model":"m","choices":[{"delta":{"content":"Hel"}}]}`,
			`: keep-alive`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"ls","arguments":"{\"p\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
		}
		for _, frame := range frames {
			if strings.HasPrefix(frame, ":") {
				_, _ = io.WriteString(w, frame+"\n\n")
				continue
			}
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
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
		t.Errorf("content = %q / streamed %q, want Hello", resp.Message.Content, text.String())
	}
	if len(calls) != 1 || calls[0].Arguments != `{"p":"/"}` || calls[0].ID != "t1" {
		t.Fatalf("tool call events = %+v", calls)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "ls" {
		t.Errorf("accumulated tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.FinishReason != inference.FinishToolCalls {
		t.Errorf("finish = %q, want tool_calls", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v, want total 3", resp.Usage)
	}
	if resp.ID != "c1" || resp.Model != "m" {
		t.Errorf("id/model = %q/%q, want c1/m", resp.ID, resp.Model)
	}
}

func TestChatStreamWithoutChunksIsEmptyResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	_, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, nil)
	if !inference.IsKind(err, inference.KindEmptyResponse) {
		t.Fatalf("kind = %q, want empty_response (err: %v)", inference.KindOf(err), err)
	}
}

func TestChatStreamHandlerErrorAbortsStream(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n")
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

func TestChatStreamStallIsClassified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Headers arrive, then the provider goes silent: exactly the case the
		// stall guard exists for. The upper bound keeps a failing guard from
		// hanging the suite.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, StallTimeout: 20 * time.Millisecond})
	_, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, nil)
	if !inference.IsKind(err, inference.KindStalled) {
		t.Fatalf("kind = %q, want stalled (err: %v)", inference.KindOf(err), err)
	}
	if !inference.Retryable(err) {
		t.Error("a stall should be retryable")
	}
}

func TestErrorStatusClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   inference.Kind
	}{
		{http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, inference.KindAuth},
		{http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, inference.KindRateLimit},
		{http.StatusBadRequest, `{"error":{"message":"nope"}}`, inference.KindInvalidRequest},
		{http.StatusBadGateway, `upstream down`, inference.KindUnavailable},
		{
			http.StatusInternalServerError,
			`{"error":{"type":"server_error","message":"Failed to parse tool call arguments as JSON: x"}}`,
			inference.KindToolCallParse,
		},
	}
	for _, tc := range cases {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
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

func TestCanceledContextIsClassified(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Chat(ctx, inference.Request{Model: "m"})
	if !inference.IsKind(err, inference.KindCanceled) {
		t.Fatalf("kind = %q, want canceled (err: %v)", inference.KindOf(err), err)
	}
}
