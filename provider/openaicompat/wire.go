package openaicompat

import (
	"cmp"
	"encoding/json"

	"github.com/rivt-ai/go-inference-router"
)

// Wire types. Pointers and omitempty are load-bearing: an unset optional field
// must be absent from the body so the provider's own default applies.

type chatRequest struct {
	Model             string         `json:"model"`
	Messages          []wireMessage  `json:"messages"`
	Tools             []wireTool     `json:"tools,omitempty"`
	ToolChoice        string         `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	ResponseFormat    *wireFormat    `json:"response_format,omitempty"`
	Temperature       *float64       `json:"temperature,omitempty"`
	TopP              *float64       `json:"top_p,omitempty"`
	MaxTokens         *int64         `json:"max_completion_tokens,omitempty"`
	Stop              []string       `json:"stop,omitempty"`
	Seed              *int64         `json:"seed,omitempty"`
	Stream            bool           `json:"stream,omitempty"`
	StreamOptions     *streamOptions `json:"stream_options,omitempty"`
	Extra             map[string]any `json:"-"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireFormat struct {
	Type       string          `json:"type"`
	JSONSchema *wireJSONSchema `json:"json_schema,omitempty"`
}

type wireJSONSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	// Reasoning replays an assistant turn's own thinking back to the provider.
	// Servers that do not read it ignore the field, so it is always sent when
	// the caller kept it.
	Reasoning string `json:"reasoning_content,omitempty"`
	// ReasoningField repeats it under "reasoning": current vLLM reads only
	// that key on input, llama.cpp and most chat templates read
	// reasoning_content first, so a template sees it once either way.
	ReasoningField string `json:"reasoning,omitempty"`
}

type wireToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolSpec `json:"function"`
}

type wireToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type wireUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type chatResponse struct {
	ID                string          `json:"id"`
	Model             string          `json:"model"`
	SystemFingerprint string          `json:"system_fingerprint"`
	Timings           json.RawMessage `json:"timings"`
	Choices           []struct {
		FinishReason string          `json:"finish_reason"`
		StopReason   json.RawMessage `json:"stop_reason"`
		Logprobs     json.RawMessage `json:"logprobs"`
		Message      struct {
			Content        string         `json:"content"`
			Reasoning      string         `json:"reasoning_content"`
			ReasoningField string         `json:"reasoning"`
			ToolCalls      []wireToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage wireUsage `json:"usage"`
}

type chatChunk struct {
	ID                string          `json:"id"`
	Model             string          `json:"model"`
	SystemFingerprint string          `json:"system_fingerprint"`
	Timings           json.RawMessage `json:"timings"`
	Choices           []struct {
		FinishReason string          `json:"finish_reason"`
		StopReason   json.RawMessage `json:"stop_reason"`
		Logprobs     json.RawMessage `json:"logprobs"`
		Delta        struct {
			Content        string         `json:"content"`
			Reasoning      string         `json:"reasoning_content"`
			ReasoningField string         `json:"reasoning"`
			ToolCalls      []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

// MarshalJSON merges Extra into the request body at the top level. Keys that
// collide with a declared field lose, so Extra can never rewrite the model,
// messages, or stream flag.
func (r chatRequest) MarshalJSON() ([]byte, error) {
	type plain chatRequest
	encoded, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return encoded, nil
	}
	merged := map[string]json.RawMessage{}
	for key, value := range r.Extra {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		merged[key] = raw
	}
	var declared map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &declared); err != nil {
		return nil, err
	}
	for key, raw := range declared {
		merged[key] = raw
	}
	return json.Marshal(merged)
}

func buildRequest(req inference.Request, stream bool) chatRequest {
	out := chatRequest{
		Model:             req.Model,
		Messages:          encodeMessages(req.Messages),
		Tools:             encodeTools(req.Tools),
		ToolChoice:        string(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Stop:              req.Stop,
		Seed:              req.Seed,
		Stream:            stream,
		Extra:             req.Extra,
	}
	out.ResponseFormat = encodeFormat(req)
	if req.MaxOutputTokens > 0 {
		limit := int64(req.MaxOutputTokens)
		out.MaxTokens = &limit
	}
	if stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return out
}

// encodeFormat maps the neutral response format onto response_format. Both the
// unconstrained JSON mode and the schema-constrained one exist here, so unlike
// the Anthropic driver neither has to be refused.
func encodeFormat(req inference.Request) *wireFormat {
	switch req.ResponseFormat {
	case inference.FormatJSON:
		return &wireFormat{Type: string(inference.FormatJSON)}
	case inference.FormatSchema:
		if len(req.ResponseSchema) == 0 {
			return nil
		}
		name := req.SchemaName
		if name == "" {
			name = "response"
		}
		return &wireFormat{
			Type:       string(inference.FormatSchema),
			JSONSchema: &wireJSONSchema{Name: name, Schema: req.ResponseSchema, Strict: true},
		}
	default:
		return nil
	}
}

func encodeMessages(messages []inference.Message) []wireMessage {
	out := make([]wireMessage, 0, len(messages))
	for _, msg := range messages {
		wire := wireMessage{
			Role:           string(msg.Role),
			Content:        msg.Content,
			Name:           msg.Name,
			ToolCallID:     msg.ToolCallID,
			Reasoning:      msg.Reasoning,
			ReasoningField: msg.Reasoning,
		}
		for _, call := range msg.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, wireToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: wireFunction{Name: call.Name, Arguments: call.Arguments},
			})
		}
		out = append(out, wire)
	}
	return out
}

func encodeTools(tools []inference.Tool) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolSpec{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return out
}

func decodeToolCalls(calls []wireToolCall) []inference.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]inference.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, inference.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return out
}

func decodeUsage(usage wireUsage) inference.Usage {
	return inference.Usage{
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		TotalTokens:        usage.TotalTokens,
		CachedPromptTokens: usage.PromptTokensDetails.CachedTokens,
		ReasoningTokens:    usage.CompletionTokensDetails.ReasoningTokens,
	}
}

func decodeResponse(body chatResponse) *inference.Response {
	out := &inference.Response{
		ID:    body.ID,
		Model: body.Model,
		Usage: decodeUsage(body.Usage),
		Message: inference.Message{
			Role: inference.RoleAssistant,
		},
	}
	if len(body.Choices) > 0 {
		choice := body.Choices[0]
		out.FinishReason = decodeFinishReason(choice.FinishReason)
		out.Message.Content = choice.Message.Content
		out.Message.Reasoning = cmp.Or(choice.Message.Reasoning, choice.Message.ReasoningField)
		out.Message.ToolCalls = decodeToolCalls(choice.Message.ToolCalls)
		setExtra(out, "logprobs", choice.Logprobs)
		setExtra(out, "stop_reason", choice.StopReason)
	}
	setExtra(out, "system_fingerprint", body.SystemFingerprint)
	setExtra(out, "timings", body.Timings)
	return out
}

// decodeFinishReason keeps the provider's value, folding the legacy
// "function_call" into tool_calls so callers switching on FinishToolCalls see it.
func decodeFinishReason(reason string) inference.FinishReason {
	if reason == "function_call" {
		return inference.FinishToolCalls
	}
	return inference.FinishReason(reason)
}

// setExtra records an optional response field under Extra, skipping empty
// values and a JSON null so a server that omits it leaves Extra untouched.
func setExtra(out *inference.Response, key string, value any) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return
		}
	case json.RawMessage:
		if len(v) == 0 || string(v) == "null" {
			return
		}
	}
	if out.Extra == nil {
		out.Extra = map[string]any{}
	}
	out.Extra[key] = value
}
