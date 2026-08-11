package openaisdk

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

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
	}
	_, _ = io.WriteString(w, body)
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

const okReply = `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m",
	"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed",
	"content":[{"type":"output_text","text":"hi","annotations":[]}]}],
	"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1,
	"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7,
	"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":0}}}`

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
			writeJSON(w, http.StatusInternalServerError, `{"error":{"message":"retry"}}`)
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

func TestChatTargetsTheResponsesEndpoint(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %q, want /responses — this driver speaks the Responses API, not chat completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth header = %q, want Bearer k", got)
		}
		writeJSON(w, 0, okReply)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
	if resp.FinishReason != inference.FinishStop {
		t.Errorf("finish = %q, want stop", resp.FinishReason)
	}
	// Prompt tokens are already whole here; the cached count is a subset.
	if resp.Usage.PromptTokens != 3 || resp.Usage.CachedPromptTokens != 2 || resp.Usage.TotalTokens != 7 {
		t.Errorf("usage = %+v, want prompt 3 / cached 2 / total 7", resp.Usage)
	}
}

// TestSystemMessagesBecomeInstructions covers the first structural translation
// this API forces: there is no system role, only a top-level instructions
// field.
func TestSystemMessagesBecomeInstructions(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model: "m",
		Messages: []inference.Message{
			inference.SystemMessage("first"),
			inference.UserMessage("hello"),
			inference.SystemMessage("second"),
		},
	}, okReply)

	if body["instructions"] != "first\n\nsecond" {
		t.Errorf("instructions = %v, want both system messages joined", body["instructions"])
	}
	input, _ := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("got %d input items, want only the user turn: %v", len(input), input)
	}
}

// TestToolCallsAndResultsAreFlatItems covers the second: an assistant turn
// with tool calls becomes sibling items, and a tool result is its own
// top-level item keyed by call_id — not nested inside a message the way both
// other wire formats do it.
func TestToolCallsAndResultsAreFlatItems(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model: "m",
		Messages: []inference.Message{
			inference.UserMessage("weather?"),
			inference.AssistantMessage("checking",
				inference.ToolCall{ID: "call_1", Name: "w", Arguments: `{"city":"Oslo"}`},
			),
			inference.ToolMessage("call_1", "cold"),
		},
	}, okReply)

	input, _ := body["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("got %d input items, want user/assistant-text/function_call/function_call_output: %v", len(input), input)
	}
	types := make([]string, len(input))
	for i, item := range input {
		entry, _ := item.(map[string]any)
		if t, ok := entry["type"].(string); ok {
			types[i] = t
		} else {
			types[i] = "message"
		}
	}
	if types[2] != "function_call" {
		t.Errorf("item 2 type = %q, want function_call", types[2])
	}
	if types[3] != "function_call_output" {
		t.Errorf("item 3 type = %q, want function_call_output", types[3])
	}

	call, _ := input[2].(map[string]any)
	if call["call_id"] != "call_1" {
		t.Errorf("call_id = %v, want call_1", call["call_id"])
	}
	// Arguments stay a JSON string on this wire format, as in the seam.
	if call["arguments"] != `{"city":"Oslo"}` {
		t.Errorf("arguments = %v, want the string verbatim", call["arguments"])
	}
	output, _ := input[3].(map[string]any)
	if output["call_id"] != "call_1" || output["output"] != "cold" {
		t.Errorf("function_call_output = %v", output)
	}
}

func TestOptionalFieldsOnlySentWhenSet(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model:    "m",
		Messages: []inference.Message{inference.UserMessage("yo")},
	}, okReply)

	for _, absent := range []string{"temperature", "top_p", "max_output_tokens", "text", "tools", "tool_choice", "instructions"} {
		if _, ok := body[absent]; ok {
			t.Errorf("unset field %q was sent: %v", absent, body[absent])
		}
	}
}

func TestSetOptionsAreEncoded(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model:           "m",
		Messages:        []inference.Message{inference.UserMessage("yo")},
		Temperature:     inference.Float(0.2),
		MaxOutputTokens: 64,
		ToolChoice:      inference.ToolChoiceRequired,
		Tools: []inference.Tool{{
			Name:        "ls",
			Description: "list",
			Parameters:  map[string]any{"type": "object"},
		}},
	}, okReply)

	if body["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want 0.2", body["temperature"])
	}
	if body["max_output_tokens"] != float64(64) {
		t.Errorf("max_output_tokens = %v, want 64", body["max_output_tokens"])
	}
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want required", body["tool_choice"])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
	// Responses flattens the function definition to the top level of the tool
	// rather than nesting it under "function".
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "ls" || tool["description"] != "list" {
		t.Errorf("tool = %v, want a flat function tool", tool)
	}
}

func TestBothStructuredOutputModesWork(t *testing.T) {
	body := captureBody(t, inference.Request{
		Model:          "m",
		Messages:       []inference.Message{inference.UserMessage("yo")},
		ResponseFormat: inference.FormatJSON,
	}, okReply)
	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Errorf("text.format = %v, want json_object", text["format"])
	}

	body = captureBody(t, inference.Request{
		Model:          "m",
		Messages:       []inference.Message{inference.UserMessage("yo")},
		ResponseFormat: inference.FormatSchema,
		ResponseSchema: map[string]any{"type": "object"},
		SchemaName:     "palette",
	}, okReply)
	text, _ = body["text"].(map[string]any)
	format, _ = text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "palette" {
		t.Fatalf("text.format = %v, want a named json_schema", text["format"])
	}
	if format["strict"] != true {
		t.Errorf("strict = %v, want true", format["strict"])
	}
}

func TestSchemaFormatWithoutSchemaIsRefused(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("an incomplete schema request must not reach the provider")
	})
	_, err := client.Chat(context.Background(), inference.Request{
		Model:          "m",
		ResponseFormat: inference.FormatSchema,
	})
	if !inference.IsKind(err, inference.KindInvalidRequest) {
		t.Fatalf("kind = %q, want invalid_request (err: %v)", inference.KindOf(err), err)
	}
}

// TestFinishReasonIsSynthesised covers the third structural difference: this
// API has no per-choice finish reason, so the neutral one is derived from the
// response status, the incompleteness reason, and the output itself.
func TestFinishReasonIsSynthesised(t *testing.T) {
	cases := []struct {
		name string
		body string
		want inference.FinishReason
	}{
		{
			name: "completed with a tool call",
			body: `{"id":"r","object":"response","created_at":1,"status":"completed","model":"m",
				"output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"w","arguments":"{}","status":"completed"}],
				"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1}`,
			want: inference.FinishToolCalls,
		},
		{
			name: "truncated by the output cap",
			body: `{"id":"r","object":"response","created_at":1,"status":"incomplete","model":"m",
				"incomplete_details":{"reason":"max_output_tokens"},
				"output":[{"id":"m1","type":"message","role":"assistant","status":"incomplete","content":[{"type":"output_text","text":"partial","annotations":[]}]}],
				"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1}`,
			want: inference.FinishLength,
		},
		{
			name: "filtered",
			body: `{"id":"r","object":"response","created_at":1,"status":"incomplete","model":"m",
				"incomplete_details":{"reason":"content_filter"},
				"output":[{"id":"m1","type":"message","role":"assistant","status":"incomplete","content":[]}],
				"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1}`,
			want: inference.FinishFilter,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, 0, tc.body)
			})
			resp, err := client.Chat(context.Background(), inference.Request{Model: "m"})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if resp.FinishReason != tc.want {
				t.Errorf("finish = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}

func TestToolCallUsesCallIDNotItemID(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 0, `{"id":"r","object":"response","created_at":1,"status":"completed","model":"m",
			"output":[{"id":"fc_item","type":"function_call","call_id":"call_1","name":"w","arguments":"{\"city\":\"Oslo\"}","status":"completed"}],
			"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	// A function_call_output must reference call_id; using the item id would
	// be silently rejected on the next turn.
	if got := resp.Message.ToolCalls[0].ID; got != "call_1" {
		t.Errorf("tool call ID = %q, want the call_id (call_1), not the item id", got)
	}
}

func writeEvents(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = io.WriteString(w, "data: "+event+"\n\n")
	}
}

func TestChatStreamEmitsDeltasAndReturnsTheFinalResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream flag = %v, want true", body["stream"])
		}
		writeEvents(w,
			`{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","created_at":1,"status":"in_progress","model":"m","output":[],"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1}}`,
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"m1","output_index":0,"content_index":0,"delta":"Hel","logprobs":[]}`,
			`{"type":"response.output_text.delta","sequence_number":2,"item_id":"m1","output_index":0,"content_index":0,"delta":"lo","logprobs":[]}`,
			`{"type":"response.output_item.done","sequence_number":3,"output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"w","arguments":"{\"city\":\"Oslo\"}","status":"completed"}}`,
			`{"type":"response.completed","sequence_number":4,"response":{"id":"r","object":"response","created_at":1,"status":"completed","model":"m","output":[{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"w","arguments":"{\"city\":\"Oslo\"}","status":"completed"}],"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"temperature":1,"top_p":1,"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`,
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
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Arguments != `{"city":"Oslo"}` {
		t.Fatalf("tool call events = %+v", calls)
	}
	if resp.FinishReason != inference.FinishToolCalls {
		t.Errorf("finish = %q, want tool_calls", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v, want total 3", resp.Usage)
	}
}

func TestStreamWithoutACompletedResponseIsProtocolError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEvents(w,
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"m1","output_index":0,"content_index":0,"delta":"a","logprobs":[]}`,
		)
	})
	_, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, nil)
	if !inference.IsKind(err, inference.KindProtocol) {
		t.Fatalf("kind = %q, want protocol (err: %v)", inference.KindOf(err), err)
	}
}

func TestStreamHandlerErrorAbortsStream(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEvents(w,
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"m1","output_index":0,"content_index":0,"delta":"a","logprobs":[]}`,
			`{"type":"response.output_text.delta","sequence_number":2,"item_id":"m1","output_index":0,"content_index":0,"delta":"b","logprobs":[]}`,
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

// TestDriverImplementsOnlyTheCapabilitiesItHas records this provider's own
// gap. Anthropic has no embeddings endpoint; OpenAI's models endpoint reports
// no context window. Neither driver pretends otherwise.
func TestDriverImplementsOnlyTheCapabilitiesItHas(t *testing.T) {
	var provider inference.Provider = New(Config{})

	if _, ok := provider.(inference.Streamer); !ok {
		t.Error("driver should implement Streamer")
	}
	if _, ok := provider.(inference.Embedder); !ok {
		t.Error("driver should implement Embedder — this provider has an embeddings endpoint")
	}
	if _, ok := provider.(inference.ModelLister); !ok {
		t.Error("driver should implement ModelLister")
	}
	if _, ok := provider.(inference.MetadataReporter); ok {
		t.Error("driver must not implement MetadataReporter — the models endpoint reports no context window")
	}
}

func TestErrorsClassifyByTypeNotStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   inference.Kind
	}{
		{401, `{"error":{"type":"authentication_error","message":"bad key"}}`, inference.KindAuth},
		{403, `{"error":{"type":"permission_error","message":"no access"}}`, inference.KindAuth},
		{429, `{"error":{"type":"rate_limit_error","message":"slow down"}}`, inference.KindRateLimit},
		{400, `{"error":{"type":"invalid_request_error","message":"bad body"}}`, inference.KindInvalidRequest},
		{400, `{"error":{"type":"invalid_request_error","code":"insufficient_quota","message":"no credit"}}`, inference.KindRateLimit},
		{500, `{"error":{"type":"server_error","message":"boom"}}`, inference.KindUnavailable},
		{502, `<html>gateway</html>`, inference.KindUnavailable},
	}
	for _, tc := range cases {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, tc.status, tc.body)
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

func TestEmbedPreservesRequestOrder(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		writeJSON(w, 0, `{"object":"list","model":"e","usage":{"prompt_tokens":1,"total_tokens":1},"data":[
			{"object":"embedding","index":1,"embedding":[0.5]},
			{"object":"embedding","index":0,"embedding":[0.25,0.75]}]}`)
	})
	vectors, err := client.Embed(context.Background(), inference.EmbeddingRequest{
		Model: "e",
		Texts: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 2 || vectors[0][0] != 0.25 || vectors[1][0] != 0.5 {
		t.Fatalf("vectors = %+v, want request order", vectors)
	}
}

func TestEmbedCountMismatchIsProtocolError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 0, `{"object":"list","model":"e","usage":{"prompt_tokens":1,"total_tokens":1},
			"data":[{"object":"embedding","index":0,"embedding":[1]}]}`)
	})
	_, err := client.Embed(context.Background(), inference.EmbeddingRequest{Texts: []string{"a", "b"}})
	if !inference.IsKind(err, inference.KindProtocol) {
		t.Fatalf("kind = %q, want protocol (err: %v)", inference.KindOf(err), err)
	}
}

func TestEmbedWithoutTextsIsInvalidRequest(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("empty embed request should not reach the provider")
	})
	_, err := client.Embed(context.Background(), inference.EmbeddingRequest{})
	if !inference.IsKind(err, inference.KindInvalidRequest) {
		t.Fatalf("kind = %q, want invalid_request (err: %v)", inference.KindOf(err), err)
	}
}

func TestListModels(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		writeJSON(w, 0, `{"object":"list","data":[
			{"id":"a","object":"model","created":1,"owned_by":"openai"},
			{"id":"b","object":"model","created":1,"owned_by":"openai"}]}`)
	})
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[0].OwnedBy != "openai" {
		t.Fatalf("models = %+v", models)
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
