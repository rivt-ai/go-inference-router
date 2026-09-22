package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	inference "github.com/rivt-ai/go-inference-router"
)

func TestChatDecodesOptionalResponseFields(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c1","model":"served-m","system_fingerprint":"fp_1",
			"choices":[{"finish_reason":"function_call",
				"logprobs":{"content":[{"token":"hi","logprob":-0.1}]},
				"message":{"content":"hi","reasoning":"vllm-style"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,
				"prompt_tokens_details":{"cached_tokens":8},
				"completion_tokens_details":{"reasoning_tokens":3}}}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m", Messages: []inference.Message{inference.UserMessage("yo")}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Model != "served-m" || resp.Message.Reasoning != "vllm-style" || resp.FinishReason != inference.FinishToolCalls {
		t.Fatalf("model=%q reasoning=%q finish=%q", resp.Model, resp.Message.Reasoning, resp.FinishReason)
	}
	if resp.Usage.CachedPromptTokens != 8 || resp.Usage.ReasoningTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Extra["system_fingerprint"] != "fp_1" {
		t.Fatalf("system_fingerprint = %#v", resp.Extra["system_fingerprint"])
	}
	if lp, _ := resp.Extra["logprobs"].(json.RawMessage); !strings.Contains(string(lp), `"token":"hi"`) {
		t.Fatalf("logprobs = %s", lp)
	}
}

func TestChatLeavesExtraUnsetWhenFieldsAbsent(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c1","model":"m","choices":[{"finish_reason":"stop","logprobs":null,"message":{"content":"hi"}}]}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m", Messages: []inference.Message{inference.UserMessage("yo")}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Extra != nil {
		t.Fatalf("extra = %#v, want nil", resp.Extra)
	}
}

func TestChatStreamDecodesOptionalResponseFields(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"id":"c1","model":"m","system_fingerprint":"fp_2","choices":[{"delta":{"reasoning":"think "},"logprobs":{"content":[{"token":"a"}]}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"more","content":"ok"},"logprobs":{"content":[{"token":"b"}]},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"completion_tokens_details":{"reasoning_tokens":2}}}`,
		} {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	var reasoning strings.Builder
	resp, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, func(e inference.Event) error {
		if e.Kind == inference.EventReasoning {
			reasoning.WriteString(e.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if reasoning.String() != "think more" || resp.Message.Reasoning != "think more" {
		t.Fatalf("streamed reasoning = %q, message reasoning = %q", reasoning.String(), resp.Message.Reasoning)
	}
	if resp.Usage.ReasoningTokens != 2 || resp.Extra["system_fingerprint"] != "fp_2" {
		t.Fatalf("usage=%+v extra=%#v", resp.Usage, resp.Extra)
	}
	lp, _ := json.Marshal(resp.Extra["logprobs"])
	if !strings.Contains(string(lp), `"token":"a"`) || !strings.Contains(string(lp), `"token":"b"`) {
		t.Fatalf("logprobs not concatenated across chunks: %s", lp)
	}
}

func TestChatEncodesReasoningForBothServerDialects(t *testing.T) {
	var raw map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"x"}}]}`)
	})
	_, err := client.Chat(context.Background(), inference.Request{Model: "m", Messages: []inference.Message{
		inference.UserMessage("yo"), {Role: inference.RoleAssistant, Content: "hi", Reasoning: "because"},
	}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	msgs := raw["messages"].([]any)
	asst := msgs[1].(map[string]any)
	if asst["reasoning_content"] != "because" || asst["reasoning"] != "because" {
		t.Fatalf("assistant message = %#v, want reasoning under both keys", asst)
	}
	if user := msgs[0].(map[string]any); user["reasoning"] != nil || user["reasoning_content"] != nil {
		t.Fatalf("user message carried reasoning: %#v", user)
	}
}

func TestChatDecodesTimingsAndStopReason(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","timings":{"prompt_ms":12.5,"predicted_ms":40,"predicted_per_second":86.2,"cache_n":900},
			"choices":[{"finish_reason":"stop","stop_reason":"</done>","message":{"content":"hi"}}]}`)
	})
	resp, err := client.Chat(context.Background(), inference.Request{Model: "m", Messages: []inference.Message{inference.UserMessage("yo")}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if tm, _ := resp.Extra["timings"].(json.RawMessage); !strings.Contains(string(tm), `"cache_n":900`) {
		t.Fatalf("timings = %s", tm)
	}
	if sr, _ := resp.Extra["stop_reason"].(json.RawMessage); string(sr) != `"</done>"` {
		t.Fatalf("stop_reason = %s", sr)
	}
}

// llama.cpp puts timings on the final stream chunk, which may carry no choices.
func TestChatStreamReadsTimingsFromChoicelessChunk(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"model":"m","choices":[{"delta":{"content":"ok"},"finish_reason":"stop","stop_reason":null}]}`,
			`{"choices":[],"timings":{"predicted_per_second":80}}`,
		} {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	resp, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if tm, _ := resp.Extra["timings"].(json.RawMessage); !strings.Contains(string(tm), "predicted_per_second") {
		t.Fatalf("timings = %s", tm)
	}
	if _, ok := resp.Extra["stop_reason"]; ok {
		t.Fatalf("null stop_reason should not be recorded: %#v", resp.Extra)
	}
}
