package anthropicsdk

import (
	"context"
	"errors"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

// classify converts an SDK error into the seam's typed error.
//
// The SDK surfaces API failures as *anthropic.Error carrying the HTTP status,
// but does not expose the body's structured `error.type` as a field. The
// adapter therefore classifies API failures by status; 529 overloaded maps to
// unavailable with the other server failures.
func (c *Client) classify(ctx context.Context, err error, stalled bool) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return c.base.Errf(driver.KindForStatus(apiErr.StatusCode), apiErr.StatusCode, apiErr.Error(), err)
	}
	return c.base.Classify(ctx, err, stalled)
}
