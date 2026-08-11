package inference

import (
	"errors"
	"fmt"
)

// Kind classifies provider-neutral failures.
type Kind string

const (
	// KindUnknown indicates an unclassified failure.
	KindUnknown Kind = "unknown"
	// KindAuth indicates rejected credentials or authorization.
	KindAuth Kind = "auth"
	// KindRateLimit indicates provider throttling.
	KindRateLimit Kind = "rate_limit"
	// KindInvalidRequest indicates an unsupported or malformed request.
	KindInvalidRequest Kind = "invalid_request"
	// KindUnavailable indicates a temporarily unavailable provider.
	KindUnavailable Kind = "unavailable"
	// KindTransport indicates a network or process transport failure.
	KindTransport Kind = "transport"
	// KindProtocol indicates a wire-protocol violation.
	KindProtocol Kind = "protocol"
	// KindStalled indicates a stream that stopped making progress.
	KindStalled Kind = "stalled"
	// KindToolCallParse indicates malformed tool-call arguments.
	KindToolCallParse Kind = "tool_call_parse"
	// KindCanceled indicates caller cancellation.
	KindCanceled Kind = "canceled"
)

// Error is a portable provider failure with optional source details.
type Error struct {
	Kind     Kind   `json:"kind"`
	Provider string `json:"provider,omitempty"`
	Status   int    `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
	Err      error  `json:"-"`
}

func (e *Error) Error() string {
	parts := e.Provider
	if parts == "" {
		parts = "llm"
	}
	parts += ": " + string(e.Kind)
	if e.Status != 0 {
		parts += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Message != "" {
		parts += ": " + e.Message
	}
	return parts
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the portable classification of err.
func KindOf(err error) Kind {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.Kind
	}
	return KindUnknown
}

// IsKind reports whether err has the requested classification.
func IsKind(err error, kind Kind) bool { return KindOf(err) == kind }

// Retryable reports whether retrying err may succeed without changing the request.
func Retryable(err error) bool {
	switch KindOf(err) {
	case KindRateLimit, KindUnavailable, KindTransport, KindStalled:
		return true
	default:
		return false
	}
}
