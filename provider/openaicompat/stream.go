package openaicompat

import (
	"encoding/json"
	"strings"

	"github.com/rivt-ai/go-inference-router"
)

// accumulator folds streamed chunks into a single Response, mirroring what a
// non-streaming call would have returned.
type accumulator struct {
	resp     inference.Response
	content  strings.Builder
	thinking strings.Builder
	calls    []inference.ToolCall
	byIndex  map[int]int
	emitted  int
	sawChunk bool
}

func newAccumulator() *accumulator {
	return &accumulator{byIndex: map[int]int{}}
}

func (a *accumulator) addFrame(frame []byte, onEvent func(inference.Event) error) error {
	var chunk chatChunk
	if err := json.Unmarshal(frame, &chunk); err != nil {
		return err
	}
	a.sawChunk = true
	if chunk.ID != "" {
		a.resp.ID = chunk.ID
	}
	if chunk.Model != "" {
		a.resp.Model = chunk.Model
	}
	if chunk.Usage != nil {
		a.resp.Usage = decodeUsage(*chunk.Usage)
	}
	if len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]
	if choice.FinishReason != "" {
		a.resp.FinishReason = inference.FinishReason(choice.FinishReason)
	}
	if err := a.addText(choice.Delta.Content, choice.Delta.Reasoning, onEvent); err != nil {
		return err
	}
	a.addToolDeltas(choice.Delta.ToolCalls)
	return nil
}

func (a *accumulator) addText(content, reasoning string, onEvent func(inference.Event) error) error {
	if reasoning != "" {
		a.thinking.WriteString(reasoning)
		if err := emit(onEvent, inference.Event{Kind: inference.EventReasoning, Text: reasoning}); err != nil {
			return err
		}
	}
	if content == "" {
		return nil
	}
	a.content.WriteString(content)
	return emit(onEvent, inference.Event{Kind: inference.EventContent, Text: content})
}

// addToolDeltas merges tool-call fragments. Providers key fragments by index;
// those that omit it (one call per chunk) fall back to append-or-extend on the
// most recent call.
func (a *accumulator) addToolDeltas(deltas []wireToolCall) {
	for _, delta := range deltas {
		index := len(a.calls)
		if delta.Index != nil {
			index = *delta.Index
		} else if len(a.calls) > 0 {
			index = len(a.calls) - 1
		}
		pos, known := a.byIndex[index]
		if !known {
			a.calls = append(a.calls, inference.ToolCall{})
			pos = len(a.calls) - 1
			a.byIndex[index] = pos
		}
		if delta.ID != "" {
			a.calls[pos].ID = delta.ID
		}
		if delta.Function.Name != "" {
			a.calls[pos].Name += delta.Function.Name
		}
		a.calls[pos].Arguments += delta.Function.Arguments
	}
}

// flush emits one EventToolCall per fully accumulated call. Tool calls arrive
// as argument fragments, so a call is only meaningful once the stream ends —
// emitting earlier would hand callers truncated JSON.
func (a *accumulator) flush(onEvent func(inference.Event) error) error {
	for a.emitted < len(a.calls) {
		call := a.calls[a.emitted]
		a.emitted++
		if err := emit(onEvent, inference.Event{Kind: inference.EventToolCall, ToolCall: call}); err != nil {
			return err
		}
	}
	return nil
}

func (a *accumulator) response() *inference.Response {
	out := a.resp
	out.Message = inference.Message{
		Role:      inference.RoleAssistant,
		Content:   a.content.String(),
		Reasoning: a.thinking.String(),
		ToolCalls: a.calls,
	}
	return &out
}

func emit(onEvent func(inference.Event) error, event inference.Event) error {
	if onEvent == nil {
		return nil
	}
	return onEvent(event)
}
