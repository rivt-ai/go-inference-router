package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/transport"
)

// Chat implements llm.Provider with a single non-streaming request.
func (c *Client) Chat(ctx context.Context, req inference.Request) (*inference.Response, error) {
	guard := c.base.Guard(ctx)
	defer guard.Stop()

	resp, err := c.post(guard, "/chat/completions", buildRequest(req, false))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var body chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		if guard.Stalled() || guard.Context().Err() != nil {
			return nil, c.base.Classify(ctx, err, guard.Stalled())
		}
		return nil, c.base.Errf(inference.KindProtocol, resp.StatusCode, "malformed completion body", err)
	}
	if len(body.Choices) == 0 {
		return nil, c.base.Errf(inference.KindEmptyResponse, resp.StatusCode, "completion contained no choices", nil)
	}
	return decodeResponse(body), nil
}

// ChatStream implements llm.Streamer. onEvent sees content, reasoning,
// and completed tool calls in arrival order; the accumulated Response is
// returned exactly as Chat would have returned it.
func (c *Client) ChatStream(
	ctx context.Context,
	req inference.Request,
	onEvent func(inference.Event) error,
) (*inference.Response, error) {
	guard := c.base.Guard(ctx)
	defer guard.Stop()

	resp, err := c.post(guard, "/chat/completions", buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	acc := newAccumulator()
	var handlerErr error
	scanErr := transport.ScanSSE(resp.Body, func(frame []byte) error {
		guard.Reset()
		if err := c.frameError(frame); err != nil {
			handlerErr = err
			return err
		}
		if err := acc.addFrame(frame, onEvent); err != nil {
			handlerErr = err
			return err
		}
		return nil
	})
	if err := c.streamFailure(ctx, guard, scanErr, handlerErr); err != nil {
		return nil, err
	}
	if !acc.sawChunk {
		// A well-formed provider always sends at least one chunk. Ending the
		// stream without any means an empty or malformed body; reporting
		// success here would surface as a phantom "model said nothing" turn.
		return nil, c.base.Errf(inference.KindEmptyResponse, resp.StatusCode, "stream ended without any chunks", nil)
	}
	if err := acc.flush(onEvent); err != nil {
		return nil, err
	}
	return acc.response(), nil
}

// streamFailure separates the three ways a stream can end badly: the caller's
// handler refused an event (returned as-is), the provider went silent, or the
// transport broke.
func (c *Client) streamFailure(ctx context.Context, guard *transport.StallGuard, scanErr, handlerErr error) error {
	if scanErr == nil {
		return nil
	}
	if handlerErr != nil {
		if handlerErr == scanErr {
			return handlerErr
		}
		return c.base.Errf(inference.KindProtocol, 0, "malformed stream chunk", handlerErr)
	}
	return c.base.Classify(ctx, scanErr, guard.Stalled())
}

// post sends a JSON body and returns a non-2xx-free response. The guard is
// consulted on failure so a watchdog cancellation is reported as a stall
// rather than as a plain cancellation.
func (c *Client) post(guard *transport.StallGuard, path string, payload any) (*http.Response, error) {
	ctx := guard.Context()
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, c.base.Errf(inference.KindInvalidRequest, 0, "encode request", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return nil, c.base.Errf(inference.KindInvalidRequest, 0, "build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	c.applyHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.base.Classify(ctx, err, guard.Stalled())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, c.httpError(resp)
	}
	return resp, nil
}
