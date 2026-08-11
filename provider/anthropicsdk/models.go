package anthropicsdk

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rivt-ai/go-inference-router"
)

// ListModels implements llm.ModelLister using the SDK's auto-pager.
func (c *Client) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	var models []inference.ModelInfo
	pager := c.sdk.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	for pager.Next() {
		entry := pager.Current()
		if entry.ID != "" {
			models = append(models, inference.ModelInfo{ID: entry.ID, OwnedBy: "anthropic"})
		}
	}
	if err := pager.Err(); err != nil {
		return models, c.classify(ctx, err, false)
	}
	return models, nil
}

// ModelMetadata implements llm.MetadataReporter. An unknown model is an
// absence rather than a failure, so callers can probe unconditionally.
func (c *Client) ModelMetadata(ctx context.Context, model string) (inference.Metadata, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return inference.Metadata{}, nil
	}
	info, err := c.sdk.Models.Get(ctx, model, anthropic.ModelGetParams{})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return inference.Metadata{}, nil
		}
		return inference.Metadata{}, c.classify(ctx, err, false)
	}
	if info.MaxInputTokens <= 0 {
		return inference.Metadata{}, nil
	}
	return inference.Metadata{
		ContextWindowTokens: info.MaxInputTokens,
		Source:              "/v1/models",
	}, nil
}
