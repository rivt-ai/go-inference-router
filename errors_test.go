package inference

import (
	"errors"
	"fmt"
	"testing"
)

func TestKindOfUnwrapsWrappedErrors(t *testing.T) {
	err := fmt.Errorf("turn failed: %w", &Error{Kind: KindRateLimit, Provider: "p", Status: 429})
	if KindOf(err) != KindRateLimit || !IsKind(err, KindRateLimit) {
		t.Fatalf("kind = %q", KindOf(err))
	}
}

func TestKindOfPlainErrorIsUnknown(t *testing.T) {
	if KindOf(errors.New("boom")) != KindUnknown || KindOf(nil) != KindUnknown {
		t.Fatal("plain and nil errors must be unknown")
	}
}

func TestRetryable(t *testing.T) {
	for _, kind := range []Kind{KindRateLimit, KindUnavailable, KindTransport, KindStalled} {
		if !Retryable(&Error{Kind: kind}) {
			t.Errorf("%q should be retryable", kind)
		}
	}
	for _, kind := range []Kind{KindAuth, KindInvalidRequest, KindProtocol, KindToolCallParse, KindCanceled} {
		if Retryable(&Error{Kind: kind}) {
			t.Errorf("%q should not be retryable", kind)
		}
	}
}

func TestErrorMessageAndCause(t *testing.T) {
	cause := errors.New("dial tcp")
	err := &Error{Kind: KindAuth, Provider: "llama", Status: 401, Message: "bad key", Err: cause}
	if got, want := err.Error(), "llama: auth (HTTP 401): bad key: dial tcp"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is should reach the wrapped cause")
	}
}

func TestErrorTextIncludesCauseOnlyWhenItAddsInformation(t *testing.T) {
	cases := []struct {
		name string
		err  Error
		want string
	}{
		{"message only", Error{Kind: KindAuth, Provider: "p", Message: "bad key"}, "p: auth: bad key"},
		{"cause only", Error{Kind: KindTransport, Provider: "p", Err: errors.New("dial tcp 1.2.3.4:443: connection refused")}, "p: transport: dial tcp 1.2.3.4:443: connection refused"},
		{"message and cause", Error{Kind: KindTransport, Provider: "p", Message: "request failed", Err: errors.New("connection refused")}, "p: transport: request failed: connection refused"},
		{"cause contained in message", Error{Kind: KindTransport, Provider: "p", Message: "request failed: connection refused", Err: errors.New("connection refused")}, "p: transport: request failed: connection refused"},
		{"neither", Error{Kind: KindTransport}, "llm: transport"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%s: Error() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
