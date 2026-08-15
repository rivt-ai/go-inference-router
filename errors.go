package inference

import (
	"errors"
	"fmt"
	"time"
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
	// KindEmptyResponse indicates a well-formed exchange that carried no
	// generation: no choices, or a stream that ended without a chunk. It is
	// distinct from KindProtocol, where the provider sent something the
	// adapter could not read at all.
	KindEmptyResponse Kind = "empty_response"
	// KindCanceled indicates caller cancellation.
	KindCanceled Kind = "canceled"
)

// ErrUnverified reports a registry manifest that failed authentication.
//
// It lives in the root module, which has no dependencies, so that a verifier
// implementation can name it without importing the router module. That keeps
// an out-of-tree verifier — see router/verify/sigstore — a leaf module
// alongside the SDK adapters rather than one that depends on router, which is
// what lets the existing release tooling publish it.
//
// Callers distinguish "not authentic" from "could not check" with errors.Is:
// a verifier that consults a network service can fail either way, and only the
// first means the artifact is untrustworthy. router/install re-exports this as
// install.ErrUnverified.
var ErrUnverified = errors.New("registry manifest failed verification")

// Error is a portable provider failure with optional source details.
type Error struct {
	Kind     Kind   `json:"kind"`
	Provider string `json:"provider,omitempty"`
	Status   int    `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
	// RetryAfter is how long the provider asked the caller to wait before
	// retrying, when it said so. Zero means no hint was given, and callers
	// should fall back to their own backoff.
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	Err        error         `json:"-"`
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
