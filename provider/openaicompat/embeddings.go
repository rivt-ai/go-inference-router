package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rivt-ai/go-inference-router"
)

type embeddingRequest struct {
	Model      string   `json:"model,omitempty"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// Embed implements llm.Embedder against POST /v1/embeddings. Vectors are
// returned in request order; a provider returning a different count is a
// protocol failure rather than a silently short result.
func (c *Client) Embed(ctx context.Context, req inference.EmbeddingRequest) ([][]float32, error) {
	if len(req.Texts) == 0 {
		return nil, c.base.Errf(inference.KindInvalidRequest, 0, "no texts to embed", nil)
	}
	guard := c.base.Guard(ctx)
	defer guard.Stop()

	payload := embeddingRequest{Model: req.Model, Input: req.Texts, Dimensions: req.Dimensions}
	resp, err := c.post(guard, "/embeddings", payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, c.base.Errf(inference.KindProtocol, resp.StatusCode, "malformed embeddings body", err)
	}
	if len(body.Data) != len(req.Texts) {
		return nil, c.base.Errf(inference.KindProtocol, resp.StatusCode,
			fmt.Sprintf("provider returned %d vectors for %d texts", len(body.Data), len(req.Texts)), nil)
	}
	vectors := make([][]float32, len(body.Data))
	for i, item := range body.Data {
		slot := i
		if item.Index >= 0 && item.Index < len(vectors) {
			slot = item.Index
		}
		vector := make([]float32, len(item.Embedding))
		for j, value := range item.Embedding {
			vector[j] = float32(value)
		}
		vectors[slot] = vector
	}
	return vectors, nil
}
