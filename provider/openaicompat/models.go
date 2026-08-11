package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/rivt-ai/go-inference-router"
)

// ListModels implements llm.ModelLister against GET /v1/models. A
// provider that does not serve the endpoint (404/405) yields no models and no
// error, so discovery stays best-effort.
func (c *Client) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	found, err := c.getJSON(ctx, c.apiBase+"/models", &body)
	if err != nil || !found {
		return nil, err
	}
	models := make([]inference.ModelInfo, 0, len(body.Data))
	for _, model := range body.Data {
		if model.ID != "" {
			models = append(models, inference.ModelInfo{ID: model.ID, OwnedBy: model.OwnedBy})
		}
	}
	return models, nil
}

// ModelMetadata implements llm.MetadataReporter against the endpoint
// named by Config.MetadataPath (llama.cpp serves "/props"). No configured
// path, or a provider that does not serve it, yields a zero Metadata and no
// error.
func (c *Client) ModelMetadata(ctx context.Context, model string) (inference.Metadata, error) {
	path := strings.TrimSpace(c.cfg.MetadataPath)
	if path == "" {
		return inference.Metadata{}, nil
	}
	endpoint := c.root + "/" + strings.TrimPrefix(path, "/")
	if model = strings.TrimSpace(model); model != "" {
		endpoint += "?" + url.Values{"model": {model}}.Encode()
	}
	var body struct {
		DefaultGenerationSettings struct {
			NContext int64 `json:"n_ctx"`
		} `json:"default_generation_settings"`
		// vLLM and friends report the window at the top level instead.
		MaxModelLen int64 `json:"max_model_len"`
	}
	found, err := c.getJSON(ctx, endpoint, &body)
	if err != nil || !found {
		return inference.Metadata{}, err
	}
	window := body.DefaultGenerationSettings.NContext
	if window <= 0 {
		window = body.MaxModelLen
	}
	if window <= 0 {
		return inference.Metadata{}, nil
	}
	return inference.Metadata{ContextWindowTokens: window, Source: path}, nil
}

// getJSON performs a GET and decodes into out. The bool reports whether the
// endpoint exists: 404 and 405 come back (false, nil) so absent optional
// endpoints are not failures.
func (c *Client) getJSON(ctx context.Context, endpoint string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, c.base.Errf(inference.KindInvalidRequest, 0, "build request", err)
	}
	c.applyHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, c.base.Classify(ctx, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, c.httpError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return false, c.base.Errf(inference.KindProtocol, resp.StatusCode, "malformed response body", err)
	}
	return true, nil
}
