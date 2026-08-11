package driver_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

func TestKindForStatus(t *testing.T) {
	tests := map[int]llm.Kind{
		http.StatusUnauthorized:    llm.KindAuth,
		http.StatusTooManyRequests: llm.KindRateLimit,
		http.StatusBadRequest:      llm.KindInvalidRequest,
		http.StatusBadGateway:      llm.KindUnavailable,
		http.StatusOK:              llm.KindUnknown,
	}
	for status, want := range tests {
		if got := driver.KindForStatus(status); got != want {
			t.Errorf("KindForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestClassifyTransportFailures(t *testing.T) {
	base := driver.New("test", 0, time.Minute)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		ctx     context.Context
		err     error
		stalled bool
		want    llm.Kind
	}{
		{"stalled", context.Background(), errors.New("read"), true, llm.KindStalled},
		{"canceled", canceled, context.Canceled, false, llm.KindCanceled},
		{"deadline", context.Background(), context.DeadlineExceeded, false, llm.KindTransport},
		{"transport", context.Background(), errors.New("read"), false, llm.KindTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := llm.KindOf(base.Classify(test.ctx, test.err, test.stalled)); got != test.want {
				t.Fatalf("kind = %q, want %q", got, test.want)
			}
		})
	}
}
