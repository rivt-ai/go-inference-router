package openaisdk

import (
	"context"

	"github.com/rivt-ai/go-inference-router"
	"github.com/openai/openai-go/responses"
)

// Chat implements llm.Provider.
func (c *Client) Chat(ctx context.Context, req inference.Request) (*inference.Response, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}
	guard := c.base.Guard(ctx)
	defer guard.Stop()

	resp, err := c.sdk.Responses.New(guard.Context(), params)
	if err != nil {
		return nil, c.classify(ctx, err, guard.Stalled())
	}
	// A response can fail server-side while still returning 200; the error is
	// carried in the body rather than the status line.
	if resp.Error.Code != "" {
		kind, ok := kindForType(string(resp.Error.Code), "")
		if !ok {
			kind = inference.KindUnavailable
		}
		return nil, c.base.Errf(kind, 0, resp.Error.Message, nil)
	}
	if len(resp.Output) == 0 && resp.Status == "completed" {
		return nil, c.base.Errf(inference.KindProtocol, 0, "response contained no output items", nil)
	}
	return decodeResponse(resp), nil
}

// ChatStream implements llm.Streamer.
//
// The Responses stream is item-oriented rather than chunk-oriented: it names
// the delta type in the event, and it delivers a fully-formed Response on
// completion. That final snapshot is what this method returns, so there is no
// accumulator to maintain — a further simplification over the Anthropic SDK
// driver, which still folds its own message.
func (c *Client) ChatStream(
	ctx context.Context,
	req inference.Request,
	onEvent func(inference.Event) error,
) (*inference.Response, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}
	guard := c.base.Guard(ctx)
	defer guard.Stop()

	stream := c.sdk.Responses.NewStreaming(guard.Context(), params)
	defer func() { _ = stream.Close() }()

	var final *responses.Response
	sawEvent := false
	for stream.Next() {
		guard.Reset()
		sawEvent = true
		event := stream.Current()
		if snapshot, err := c.handleEvent(event, onEvent); err != nil {
			return nil, err
		} else if snapshot != nil {
			final = snapshot
		}
	}
	if err := stream.Err(); err != nil {
		return nil, c.classify(ctx, err, guard.Stalled())
	}
	if !sawEvent {
		return nil, c.base.Errf(inference.KindProtocol, 0, "stream ended without any events", nil)
	}
	if final == nil {
		return nil, c.base.Errf(inference.KindProtocol, 0, "stream ended without a completed response", nil)
	}
	return decodeResponse(final), nil
}

// handleEvent translates one stream event, returning the terminal response
// snapshot when the stream carries one.
func (c *Client) handleEvent(
	event responses.ResponseStreamEventUnion,
	onEvent func(inference.Event) error,
) (*responses.Response, error) {
	switch event.Type {
	case "response.output_text.delta":
		return nil, emit(onEvent, inference.Event{Kind: inference.EventContent, Text: event.Delta.OfString})
	case "response.reasoning_summary_text.delta":
		return nil, emit(onEvent, inference.Event{Kind: inference.EventReasoning, Text: event.Delta.OfString})
	case "response.output_item.done":
		// A tool call is complete the moment its item closes, so callers get
		// it before the stream ends.
		item := event.Item
		if item.Type != "function_call" {
			return nil, nil
		}
		return nil, emit(onEvent, inference.Event{
			Kind: inference.EventToolCall,
			ToolCall: inference.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: trimmedJSON(item.Arguments),
			},
		})
	case "response.completed", "response.incomplete":
		snapshot := event.Response
		return &snapshot, nil
	case "response.failed":
		snapshot := event.Response
		kind, ok := kindForType(string(snapshot.Error.Code), "")
		if !ok {
			kind = inference.KindUnavailable
		}
		return nil, c.base.Errf(kind, 0, snapshot.Error.Message, nil)
	case "error":
		return nil, c.base.Errf(inference.KindUnavailable, 0, event.Message, nil)
	}
	return nil, nil
}

func emit(onEvent func(inference.Event) error, event inference.Event) error {
	if onEvent == nil {
		return nil
	}
	return onEvent(event)
}
