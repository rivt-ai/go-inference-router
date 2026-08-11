package anthropicsdk

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rivt-ai/go-inference-router"
)

// Chat implements llm.Provider.
func (c *Client) Chat(ctx context.Context, req inference.Request) (*inference.Response, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}
	guard := c.base.Guard(ctx)
	defer guard.Stop()

	msg, err := c.sdk.Messages.New(guard.Context(), params)
	if err != nil {
		return nil, c.classify(ctx, err, guard.Stalled())
	}
	if len(msg.Content) == 0 && msg.StopReason != anthropic.StopReasonRefusal {
		// A refusal legitimately returns empty content; anything else that
		// does is a malformed message.
		return nil, c.base.Errf(inference.KindProtocol, 0, "message contained no content blocks", nil)
	}
	return decodeMessage(msg), nil
}

// ChatStream implements llm.Streamer.
//
// The SDK accumulates the final message itself, so this method translates
// events into the neutral stream and returns the SDK-built response.
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

	stream := c.sdk.Messages.NewStreaming(guard.Context(), params)
	defer func() { _ = stream.Close() }()

	var acc anthropic.Message
	sawEvent := false
	for stream.Next() {
		guard.Reset()
		sawEvent = true
		event := stream.Current()
		if err := acc.Accumulate(event); err != nil {
			return nil, c.base.Errf(inference.KindProtocol, 0, "accumulate stream event", err)
		}
		if err := emitEvent(&acc, event, onEvent); err != nil {
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, c.classify(ctx, err, guard.Stalled())
	}
	if !sawEvent {
		return nil, c.base.Errf(inference.KindProtocol, 0, "stream ended without any events", nil)
	}
	return decodeMessage(&acc), nil
}

// emitEvent turns one SDK stream event into neutral events. Tool calls are
// emitted at content_block_stop, reading the arguments the accumulator has
// already assembled for that block — so this driver never merges fragments by
// hand.
func emitEvent(acc *anthropic.Message, event anthropic.MessageStreamEventUnion, onEvent func(inference.Event) error) error {
	switch event.Type {
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			return emit(onEvent, inference.Event{Kind: inference.EventContent, Text: event.Delta.Text})
		case "thinking_delta":
			return emit(onEvent, inference.Event{Kind: inference.EventReasoning, Text: event.Delta.Thinking})
		}
	case "content_block_stop":
		index := int(event.Index)
		if index < 0 || index >= len(acc.Content) {
			return nil
		}
		block := acc.Content[index]
		if block.Type != "tool_use" {
			return nil
		}
		arguments := string(block.Input)
		if arguments == "" {
			arguments = "{}"
		}
		return emit(onEvent, inference.Event{
			Kind:     inference.EventToolCall,
			ToolCall: inference.ToolCall{ID: block.ID, Name: block.Name, Arguments: arguments},
		})
	}
	return nil
}

func emit(onEvent func(inference.Event) error, event inference.Event) error {
	if onEvent == nil {
		return nil
	}
	return onEvent(event)
}
