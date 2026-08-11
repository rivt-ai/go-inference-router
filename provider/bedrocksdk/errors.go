package bedrocksdk

import (
	"context"
	"errors"
	"strings"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/aws/smithy-go"
)

func (c *Client) classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return c.base.Classify(ctx, ctx.Err(), false)
	}
	kind := llm.KindTransport
	message := err.Error()
	var api smithy.APIError
	if errors.As(err, &api) {
		code := strings.ToLower(api.ErrorCode())
		message = api.ErrorMessage()
		switch {
		case strings.Contains(code, "access"), strings.Contains(code, "auth"), strings.Contains(code, "credential"):
			kind = llm.KindAuth
		case strings.Contains(code, "throttl"), strings.Contains(code, "limit"):
			kind = llm.KindRateLimit
		case strings.Contains(code, "validation"), strings.Contains(code, "resource"):
			kind = llm.KindInvalidRequest
		case strings.Contains(code, "unavailable"), strings.Contains(code, "internal"), strings.Contains(code, "timeout"):
			kind = llm.KindUnavailable
		default:
			kind = llm.KindUnknown
		}
	}
	if kind == llm.KindTransport {
		return c.base.Classify(ctx, err, false)
	}
	return c.base.Errf(kind, 0, message, err)
}
