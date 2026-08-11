package openaisdk

import (
	"strings"

	"github.com/rivt-ai/go-inference-router"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// buildParams translates a neutral request into Responses API params.
//
// The Responses API is not chat-completions with new names. Its structural
// differences from the seam are closer to Anthropic's than to the compat
// format: the system prompt is a top-level `instructions` field rather than a
// role, tool calls and their results are flat top-level input items rather
// than message content, and completion status is a response-level field rather
// than a per-choice finish reason.
func (c *Client) buildParams(req inference.Request) (responses.ResponseNewParams, error) {
	instructions, input := encodeInput(req.Messages)
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(req.Model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Tools: encodeTools(req.Tools),
	}
	if instructions != "" {
		params.Instructions = openai.String(instructions)
	}
	if choice, ok := encodeToolChoice(req.ToolChoice); ok {
		params.ToolChoice = choice
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = openai.Float(*req.TopP)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxOutputTokens))
	}
	format, err := c.encodeFormat(req)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	if format != nil {
		params.Text = responses.ResponseTextConfigParam{Format: *format}
	}
	return params, nil
}

// encodeFormat maps the neutral response format onto `text.format`. This API
// offers both an unconstrained JSON mode and a schema-constrained one, so
// unlike the Anthropic driver neither has to be refused.
func (c *Client) encodeFormat(req inference.Request) (*responses.ResponseFormatTextConfigUnionParam, error) {
	switch req.ResponseFormat {
	case inference.FormatText:
		return nil, nil
	case inference.FormatJSON:
		return &responses.ResponseFormatTextConfigUnionParam{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		}, nil
	case inference.FormatSchema:
		if len(req.ResponseSchema) == 0 {
			return nil, c.base.Errf(inference.KindInvalidRequest, 0,
				"ResponseFormat is json_schema but ResponseSchema is empty", nil)
		}
		name := req.SchemaName
		if name == "" {
			name = "response"
		}
		return &responses.ResponseFormatTextConfigUnionParam{
			OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
				Name:   name,
				Schema: req.ResponseSchema,
				Strict: openai.Bool(true),
			},
		}, nil
	default:
		return nil, c.base.Errf(inference.KindInvalidRequest, 0,
			"unsupported response format "+string(req.ResponseFormat), nil)
	}
}

// encodeInput splits the neutral message list into top-level instructions and
// a flat item list.
//
// Two translations are load-bearing and specific to this API. System messages
// are hoisted into `instructions` — including any mid-conversation ones, which
// therefore apply from the start of the exchange. And an assistant turn that
// carries tool calls becomes *several* sibling items (an optional message plus
// one function_call each) rather than one message with nested calls, with tool
// results arriving as their own top-level function_call_output items keyed by
// call id.
func encodeInput(messages []inference.Message) (string, responses.ResponseInputParam) {
	var instructions []string
	input := make(responses.ResponseInputParam, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case inference.RoleSystem:
			if msg.Content != "" {
				instructions = append(instructions, msg.Content)
			}
		case inference.RoleTool:
			input = append(input,
				responses.ResponseInputItemParamOfFunctionCallOutput(msg.ToolCallID, msg.Content))
		case inference.RoleAssistant:
			if msg.Content != "" {
				input = append(input,
					responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleAssistant))
			}
			for _, call := range msg.ToolCalls {
				input = append(input, responses.ResponseInputItemParamOfFunctionCall(
					trimmedJSON(call.Arguments), call.ID, call.Name))
			}
		default:
			input = append(input,
				responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleUser))
		}
	}
	return strings.Join(instructions, "\n\n"), input
}

func encodeTools(tools []inference.Tool) []responses.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		function := &responses.FunctionToolParam{
			Name:       tool.Name,
			Parameters: tool.Parameters,
			// The API requires this field; false keeps the permissive
			// behaviour a neutral schema implies.
			Strict: openai.Bool(false),
		}
		if tool.Description != "" {
			function.Description = openai.String(tool.Description)
		}
		out = append(out, responses.ToolUnionParam{OfFunction: function})
	}
	return out
}

func encodeToolChoice(choice inference.ToolChoice) (responses.ResponseNewParamsToolChoiceUnion, bool) {
	switch choice {
	case inference.ToolChoiceAuto, inference.ToolChoiceNone, inference.ToolChoiceRequired:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptions(choice)),
		}, true
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}, false
	}
}

// decodeUsage maps token accounting. Prompt tokens are already whole here and
// the cached portion is a subset, not an addend — the opposite of Anthropic's
// split accounting.
func decodeUsage(usage responses.ResponseUsage) inference.Usage {
	return inference.Usage{
		PromptTokens:       usage.InputTokens,
		CompletionTokens:   usage.OutputTokens,
		TotalTokens:        usage.TotalTokens,
		CachedPromptTokens: usage.InputTokensDetails.CachedTokens,
	}
}

// decodeResponse folds the output item list into the neutral response. Text,
// reasoning summaries, and tool calls arrive as sibling items rather than as
// one message, so they are gathered rather than read from a single choice.
func decodeResponse(resp *responses.Response) *inference.Response {
	out := &inference.Response{
		ID:      resp.ID,
		Model:   string(resp.Model),
		Usage:   decodeUsage(resp.Usage),
		Message: inference.Message{Role: inference.RoleAssistant},
	}
	var text, reasoning strings.Builder
	sawToolCall := false
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				text.WriteString(content.Text)
			}
		case "reasoning":
			for _, summary := range item.Summary {
				reasoning.WriteString(summary.Text)
			}
		case "function_call":
			sawToolCall = true
			out.Message.ToolCalls = append(out.Message.ToolCalls, inference.ToolCall{
				// call_id, not id: the id names the output item, the call_id
				// is what a function_call_output must reference.
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: trimmedJSON(item.Arguments),
			})
		}
	}
	out.Message.Content = text.String()
	out.Message.Reasoning = reasoning.String()
	out.FinishReason = decodeFinishReason(resp, sawToolCall)
	return out
}

// decodeFinishReason synthesises the neutral finish reason. This API reports a
// response-level status plus an incompleteness reason rather than a per-choice
// finish reason, so "stopped to call a tool" has to be inferred from the
// output itself.
func decodeFinishReason(resp *responses.Response, sawToolCall bool) inference.FinishReason {
	switch resp.Status {
	case "incomplete":
		switch resp.IncompleteDetails.Reason {
		case "max_output_tokens":
			return inference.FinishLength
		case "content_filter":
			return inference.FinishFilter
		default:
			return inference.FinishReason(resp.IncompleteDetails.Reason)
		}
	case "completed":
		if sawToolCall {
			return inference.FinishToolCalls
		}
		return inference.FinishStop
	case "":
		return inference.FinishUnknown
	default:
		// failed, cancelled, in_progress, queued — no neutral equivalent, so
		// passed through rather than flattened.
		return inference.FinishReason(resp.Status)
	}
}

// trimmedJSON normalises tool-call arguments that arrive empty. A no-argument
// call sends an empty string, and callers expect valid JSON.
func trimmedJSON(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return "{}"
	}
	return arguments
}
