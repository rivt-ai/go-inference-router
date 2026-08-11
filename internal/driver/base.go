// Package driver centralizes behavior shared by provider adapters.
package driver

import (
	"context"
	"errors"
	"net/http"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/transport"
)

// Base carries provider identity and neutral transport behavior.
type Base struct {
	Name         string
	StallTimeout time.Duration
}

// New creates a Base and applies fallback when stallTimeout is zero.
func New(name string, stallTimeout, fallback time.Duration) Base {
	if stallTimeout == 0 {
		stallTimeout = fallback
	}
	return Base{Name: name, StallTimeout: stallTimeout}
}

// Errf creates a provider-neutral typed error.
func (b Base) Errf(kind llm.Kind, status int, message string, err error) *llm.Error {
	return &llm.Error{Kind: kind, Provider: b.Name, Status: status, Message: message, Err: err}
}

// Guard creates a watchdog for silence between provider reads.
func (b Base) Guard(parent context.Context) *transport.StallGuard {
	return transport.NewStallGuard(parent, b.StallTimeout)
}

// Classify converts a non-API transport failure into a neutral error.
func (b Base) Classify(ctx context.Context, err error, stalled bool) error {
	if err == nil {
		return nil
	}
	switch {
	case stalled:
		return b.Errf(llm.KindStalled, 0, "provider sent no data for "+b.StallTimeout.String(), err)
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		return b.Errf(llm.KindCanceled, 0, "", err)
	case errors.Is(err, context.DeadlineExceeded):
		return b.Errf(llm.KindTransport, 0, "request deadline exceeded", err)
	default:
		return b.Errf(llm.KindTransport, 0, "", err)
	}
}

// KindForStatus maps common HTTP status classes onto neutral error kinds.
func KindForStatus(status int) llm.Kind {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return llm.KindAuth
	case status == http.StatusTooManyRequests:
		return llm.KindRateLimit
	case status >= 500:
		return llm.KindUnavailable
	case status >= 400:
		return llm.KindInvalidRequest
	default:
		return llm.KindUnknown
	}
}
