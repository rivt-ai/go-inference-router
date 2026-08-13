package openaicompat

import (
	"context"
	"errors"
	"io"
	"net/http"
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
