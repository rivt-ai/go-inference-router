package openaisdk

import (
	"context"
	"errors"

	"github.com/openai/openai-go"
	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

// classify converts an SDK error into the seam's typed error.
//
// This SDK exposes the API's structured error `type` and `code` alongside the
// status, so classification reads the contract first and falls back to the
// status. The Anthropic SDK does not currently expose an equivalent taxonomy.
func (c *Client) classify(ctx context.Context, err error, stalled bool) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		kind, ok := kindForType(apiErr.Type, apiErr.Code)
		if !ok {
			kind = driver.KindForStatus(apiErr.StatusCode)
		}
		return c.base.Errf(kind, apiErr.StatusCode, apiErr.Message, err)
	}
	return c.base.Classify(ctx, err, stalled)
}

// kindForType maps the API's own error taxonomy onto neutral kinds.
func kindForType(errType, code string) (inference.Kind, bool) {
	switch errType {
	case "authentication_error":
		return inference.KindAuth, true
	case "permission_error":
		return inference.KindAuth, true
	case "rate_limit_error":
		return inference.KindRateLimit, true
	case "invalid_request_error":
		// Quota exhaustion arrives as an invalid request with a distinguishing
		// code; it is a throttle, not a malformed call, so callers should back
		// off rather than treat it as a bug in their request.
		if code == "insufficient_quota" {
			return inference.KindRateLimit, true
		}
		return inference.KindInvalidRequest, true
	case "not_found_error":
		return inference.KindInvalidRequest, true
	case "server_error", "api_error":
		return inference.KindUnavailable, true
	default:
		return inference.KindUnknown, false
	}
}
