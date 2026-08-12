//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rivt-ai/go-inference-router"
)

func TestChatProducesTokensAndUsage(t *testing.T) {
	client := sharedServer.client()

	resp, err := client.Chat(testContext(t), request("Name one colour. Answer with the word only."))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if strings.TrimSpace(resp.Message.Content) == "" {
		t.Error("model generated no content")
	}
	if resp.Message.Role != inference.RoleAssistant {
		t.Errorf("role = %q, want assistant", resp.Message.Role)
	}
	if resp.Usage.PromptTokens <= 0 || resp.Usage.CompletionTokens <= 0 {
		t.Errorf("usage = %+v, want non-zero prompt and completion tokens", resp.Usage)
	}
	if resp.FinishReason != inference.FinishStop && resp.FinishReason != inference.FinishLength {
		t.Errorf("finish reason = %q, want stop or length", resp.FinishReason)
	}
	if resp.Model == "" {
		t.Error("response carried no model name")
	}
}

// TestChatStreamMatchesChat is the seam's central promise: a caller that wants
// incremental output and a caller that does not must end up with the same
// answer. Greedy sampling with a fixed seed makes that assertable — but only
// once llama.cpp's own prompt-cache reuse is out of the way. Both calls below
// send the identical prompt back-to-back on the same server, and llama.cpp's
// server defaults to cache_prompt=true: the second call would reuse the KV
// cache the first one left behind instead of recomputing it, and the server's
// own docs note that reused and freshly computed logits are not guaranteed
// bit-identical. cache_prompt: false forces both calls through the same
// from-scratch computation, which is what "greedy and deterministic" actually
// requires here.
func TestChatStreamMatchesChat(t *testing.T) {
	client := sharedServer.client()
	prompt := request("Count: one, two,")
	prompt.Extra = map[string]any{"cache_prompt": false}

	batch, err := client.Chat(testContext(t), prompt)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	var streamed strings.Builder
	events := 0
	streamedResp, err := client.ChatStream(testContext(t), prompt, func(e inference.Event) error {
		if e.Kind == inference.EventContent {
			events++
			streamed.WriteString(e.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if events < 2 {
		t.Errorf("saw %d content events, want the response delivered incrementally", events)
	}
	if streamed.String() != streamedResp.Message.Content {
		t.Errorf("accumulated response does not match the streamed fragments:\n streamed: %q\n response: %q",
			streamed.String(), streamedResp.Message.Content)
	}
	if streamedResp.Message.Content != batch.Message.Content {
		t.Errorf("streaming and non-streaming disagree under greedy sampling:\n stream: %q\n batch:  %q",
			streamedResp.Message.Content, batch.Message.Content)
	}
	if streamedResp.Usage.CompletionTokens <= 0 {
		t.Errorf("streamed usage = %+v, want completion tokens (stream_options.include_usage)", streamedResp.Usage)
	}
}

func TestMaxOutputTokensIsHonoured(t *testing.T) {
	client := sharedServer.client()
	req := request("Write several sentences about the sea.")
	req.MaxOutputTokens = 8

	resp, err := client.Chat(testContext(t), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.CompletionTokens > 8 {
		t.Errorf("completion tokens = %d, want <= 8", resp.Usage.CompletionTokens)
	}
	if resp.FinishReason != inference.FinishLength {
		t.Errorf("finish reason = %q, want length", resp.FinishReason)
	}
}

// TestJSONSchemaResponseFormatProducesJSON exercises the strongest neutral
// structured-output constraint. llama.cpp enforces it with a grammar, so even
// a 135M model cannot emit invalid JSON — a failure means the schema was lost.
func TestJSONSchemaResponseFormatProducesJSON(t *testing.T) {
	client := sharedServer.client()
	req := request(`Reply with a JSON object holding the key "colour".`)
	req.ResponseFormat = inference.FormatSchema
	req.ResponseSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"colour": map[string]any{"type": "string"},
		},
		"required":             []string{"colour"},
		"additionalProperties": false,
	}
	req.MaxOutputTokens = 96

	resp, err := client.Chat(testContext(t), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(resp.Message.Content), &decoded); err != nil {
		t.Fatalf("response was not a JSON object: %v\ncontent: %q", err, resp.Message.Content)
	}
}

// TestExtraReachesTheProvider proves Request.Extra is a real escape hatch and
// not decoration: a GBNF grammar is llama.cpp-specific, has no neutral field,
// and constrains the output to exactly one string. If Extra were dropped, the
// model would answer with something else.
func TestExtraReachesTheProvider(t *testing.T) {
	client := sharedServer.client()
	req := request("Say anything at all.")
	req.Extra = map[string]any{"grammar": `root ::= "PINNED"`}

	resp, err := client.Chat(testContext(t), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := strings.TrimSpace(resp.Message.Content); got != "PINNED" {
		t.Errorf("content = %q, want PINNED — the grammar in Request.Extra did not take effect", got)
	}
}

// TestToolsAreAcceptedByTheProvider guards tool serialization against a real
// chat template. The pinned model is far too small to choose tools reliably, so
// the assertion is that the request is well-formed, not that a call happens.
func TestToolsAreAcceptedByTheProvider(t *testing.T) {
	client := sharedServer.client()
	req := request("What is the weather in Oslo?")
	req.Tools = []inference.Tool{{
		Name:        "get_weather",
		Description: "Look up the current weather for a city",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	}}

	resp, err := client.Chat(testContext(t), req)
	if err != nil {
		t.Fatalf("Chat with tools: %v", err)
	}
	if resp.Message.Content == "" && len(resp.Message.ToolCalls) == 0 {
		t.Error("response carried neither content nor tool calls")
	}
	for _, call := range resp.Message.ToolCalls {
		if call.Name == "" {
			t.Errorf("tool call has no name: %+v", call)
		}
	}
}

// TestCancellationMidStreamIsClassified distinguishes the caller hanging up
// from the provider failing — the two look identical at the transport layer.
func TestCancellationMidStreamIsClassified(t *testing.T) {
	client := sharedServer.client()
	req := request("Write a long story about a lighthouse.")
	req.MaxOutputTokens = 256

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := client.ChatStream(ctx, req, func(inference.Event) error {
		cancel()
		return nil
	})
	if !inference.IsKind(err, inference.KindCanceled) {
		t.Fatalf("kind = %q, want canceled (err: %v)", inference.KindOf(err), err)
	}
	if inference.Retryable(err) {
		t.Error("caller cancellation must not be reported as retryable")
	}
}

// TestInvalidRequestIsClassified checks the error path against the real server
// rather than a fake we shaped ourselves. An invalid provider-specific response
// format deterministically reaches llama.cpp's HTTP 400 validation path.
func TestInvalidRequestIsClassified(t *testing.T) {
	client := sharedServer.client()
	req := request("Say anything.")
	req.Extra = map[string]any{
		"response_format": map[string]any{"type": "definitely_invalid"},
	}

	_, err := client.Chat(testContext(t), req)
	if !inference.IsKind(err, inference.KindInvalidRequest) {
		t.Fatalf("kind = %q, want invalid_request (err: %v)", inference.KindOf(err), err)
	}
	var infErr *inference.Error
	if !asInferenceError(err, &infErr) {
		t.Fatalf("error was not an *llm.Error: %v", err)
	}
	if infErr.Status != 400 {
		t.Errorf("status = %d, want 400", infErr.Status)
	}
	if infErr.Message == "" {
		t.Error("error carried no provider explanation")
	}
}
