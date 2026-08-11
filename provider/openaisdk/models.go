package openaisdk

import (
	"context"
	"fmt"

	"github.com/rivt-ai/go-inference-router"
	"github.com/openai/openai-go"
)

// Embed implements llm.Embedder. Vectors come back in request order; a
// provider returning a different count is a protocol failure rather than a
// silently short result.
func (c *Client) Embed(ctx context.Context, req inference.EmbeddingRequest) ([][]float32, error) {
	if len(req.Texts) == 0 {
		return nil, c.base.Errf(inference.KindInvalidRequest, 0, "no texts to embed", nil)
	}
	params := openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(req.Model),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: req.Texts},
	}
	if req.Dimensions > 0 {
		params.Dimensions = openai.Int(int64(req.Dimensions))
	}
	resp, err := c.sdk.Embeddings.New(ctx, params)
	if err != nil {
		return nil, c.classify(ctx, err, false)
	}
	if len(resp.Data) != len(req.Texts) {
		return nil, c.base.Errf(inference.KindProtocol, 0,
			fmt.Sprintf("provider returned %d vectors for %d texts", len(resp.Data), len(req.Texts)), nil)
	}
	vectors := make([][]float32, len(resp.Data))
	for i, item := range resp.Data {
		slot := i
		if index := int(item.Index); index >= 0 && index < len(vectors) {
			slot = index
		}
		vector := make([]float32, len(item.Embedding))
		for j, value := range item.Embedding {
			vector[j] = float32(value)
		}
		vectors[slot] = vector
	}
	return vectors, nil
}

// ListModels implements llm.ModelLister. The SDK's auto-pager walks the
// full list.
func (c *Client) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	var models []inference.ModelInfo
	pager := c.sdk.Models.ListAutoPaging(ctx)
	for pager.Next() {
		entry := pager.Current()
		if entry.ID != "" {
			models = append(models, inference.ModelInfo{ID: entry.ID, OwnedBy: entry.OwnedBy})
		}
	}
	if err := pager.Err(); err != nil {
		return models, c.classify(ctx, err, false)
	}
	return models, nil
}
