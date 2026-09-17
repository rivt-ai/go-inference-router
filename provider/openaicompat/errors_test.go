package openaicompat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	inference "github.com/rivt-ai/go-inference-router"
)

func TestRetryAfterParsesDelaySeconds(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"1", time.Second},
		{"120", 2 * time.Minute},
		{" 5 ", 5 * time.Second},
		// Absent, non-numeric, and non-positive values yield no hint so the
		// caller falls back to its own backoff rather than a wrong delay.
		{"", 0},
		{"0", 0},
		{"-3", 0},
		{"soon", 0},
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0},
	}
	for _, tc := range cases {
		if got := retryAfter(tc.header); got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestHTTPErrorCarriesRetryAfter(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","message":"slow down"}}`)
	})

	_, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if !inference.IsKind(err, inference.KindRateLimit) {
		t.Fatalf("kind = %q, want rate_limit (err: %v)", inference.KindOf(err), err)
	}
	var typed *inference.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error is not *inference.Error: %v", err)
	}
	if typed.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %v, want 2s", typed.RetryAfter)
	}
}

func TestHTTPErrorWithoutRetryAfterReportsNoHint(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","message":"slow down"}}`)
	})

	_, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	var typed *inference.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error is not *inference.Error: %v", err)
	}
	if typed.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want 0", typed.RetryAfter)
	}
}

// A body the adapter cannot read at all stays KindProtocol, so it remains
// distinguishable from a well-formed response that simply carried no
// generation (KindEmptyResponse).
func TestMalformedBodyStaysProtocolError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{not json`)
	})

	_, err := client.Chat(context.Background(), inference.Request{Model: "m"})
	if !inference.IsKind(err, inference.KindProtocol) {
		t.Fatalf("kind = %q, want protocol (err: %v)", inference.KindOf(err), err)
	}
}

func TestStreamedProviderErrorIsNotSuccess(t *testing.T) {
	cases := []struct {
		name, body, message string
		kind                inference.Kind
		status              int
	}{
		{"llama numeric code", `{"error":{"code":500,"message":"Context size has been exceeded.","type":"server_error"}}`, "Context size has been exceeded.", inference.KindUnavailable, 500},
		{"string status", `{"error":{"code":"429","message":"slow down"}}`, "slow down", inference.KindRateLimit, 429},
		{"symbolic code", `{"error":{"code":"context_length_exceeded","message":"context length exceeded","type":"invalid_request_error"}}`, "context length exceeded", inference.KindInvalidRequest, 400},
		{"tool parse", `{"error":{"code":500,"message":"Failed to parse tool call arguments as JSON"}}`, "Failed to parse tool call arguments as JSON", inference.KindToolCallParse, 500},
		{"unknown", `{"error":{"message":"provider failed"}}`, "provider failed", inference.KindUnknown, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, partial := range []bool{false, true} {
				client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					if partial {
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
					}
					_, _ = io.WriteString(w, "event: error\ndata: "+tc.body+"\n\ndata: [DONE]\n\n")
				})
				var content string
				resp, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, func(e inference.Event) error { content += e.Text; return nil })
				var typed *inference.Error
				if resp != nil || !errors.As(err, &typed) {
					t.Fatalf("partial=%v: response=%+v error=%v, want typed provider failure", partial, resp, err)
				}
				if typed.Kind != tc.kind || typed.Status != tc.status || typed.Provider != client.Name() {
					t.Fatalf("wrong classification: %+v", typed)
				}
				if !strings.Contains(typed.Message, tc.message) {
					t.Fatalf("error detail lost: %v", err)
				}
				if partial && content != "partial" {
					t.Fatalf("partial content=%q", content)
				}
			}
		})
	}
}

func TestMalformedStreamErrorIsProtocol(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"error\":\"bad envelope\"}\n\n")
	})
	_, err := client.ChatStream(context.Background(), inference.Request{Model: "m"}, nil)
	if !inference.IsKind(err, inference.KindProtocol) {
		t.Fatalf("want protocol error, got %v", err)
	}
}
