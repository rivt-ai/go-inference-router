package anthropicsdk

import (
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rivt-ai/go-inference-router"
)

// buildParams translates a neutral request into Anthropic SDK parameters.
func (c *Client) buildParams(req inference.Request) (anthropic.MessageNewParams, error) {
	system, systemCached, messages := encodeMessages(req.Messages)
	params := anthropic.MessageNewParams{
		Model:         anthropic.Model(req.Model),
		MaxTokens:     c.maxTokens(req),
		Messages:      messages,
		StopSequences: req.Stop,
		Tools:         encodeTools(req.Tools),
		ToolChoice:    encodeToolChoice(req.ToolChoice),
	}
	if len(system) > 0 {
		block := anthropic.TextBlockParam{Text: strings.Join(system, "\n\n")}
		// System text is joined into one block, so any breakpoint among the
		// system messages becomes a breakpoint after all of them. Anthropic
		// caches tools before system, so this covers the tool definitions too.
		if systemCached {
			block.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		params.System = []anthropic.TextBlockParam{block}
	}
	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = anthropic.Float(*req.TopP)
	}
	format, err := c.encodeFormat(req)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}
	if format != nil {
		params.OutputConfig = anthropic.OutputConfigParam{Format: *format}
	}
	return params, nil
}

// encodeFormat refuses unconstrained JSON because Anthropic only supports
// schema-constrained structured output.
func (c *Client) encodeFormat(req inference.Request) (*anthropic.JSONOutputFormatParam, error) {
	switch req.ResponseFormat {
	case inference.FormatText:
		return nil, nil
	case inference.FormatSchema:
		if len(req.ResponseSchema) == 0 {
			return nil, c.base.Errf(inference.KindInvalidRequest, 0,
				"ResponseFormat is json_schema but ResponseSchema is empty", nil)
		}
		return &anthropic.JSONOutputFormatParam{Schema: req.ResponseSchema}, nil
	case inference.FormatJSON:
		return nil, c.base.Errf(inference.KindInvalidRequest, 0,
			"provider has no unconstrained JSON mode: set ResponseFormat to json_schema with a ResponseSchema", nil)
	default:
		return nil, c.base.Errf(inference.KindInvalidRequest, 0,
			"unsupported response format "+string(req.ResponseFormat), nil)
	}
}

// encodeMessages performs the two structural translations this API needs:
// system messages are hoisted out of the message list (there is no system
// role), and a run of consecutive tool messages is coalesced into one user
// message, because splitting parallel tool results across messages teaches the
// model to stop making parallel calls.
func encodeMessages(messages []inference.Message) ([]string, bool, []anthropic.MessageParam) {
	var system []string
	var systemCached bool
	out := make([]anthropic.MessageParam, 0, len(messages))
	var pendingResults []anthropic.ContentBlockParamUnion

	flush := func() {
		if len(pendingResults) > 0 {
			out = append(out, anthropic.NewUserMessage(pendingResults...))
			pendingResults = nil
		}
	}

	for _, msg := range messages {
		if msg.Role == inference.RoleTool {
			pendingResults = append(pendingResults,
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false))
			if cacheBreakpoint(msg) {
				markBlock(&pendingResults[len(pendingResults)-1])
			}
			continue
		}
		flush()
		switch msg.Role {
		case inference.RoleSystem:
			if msg.Content != "" {
				system = append(system, msg.Content)
			}
			systemCached = systemCached || cacheBreakpoint(msg)
		case inference.RoleAssistant:
			if blocks := encodeAssistant(msg); len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		default:
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))
		}
		if msg.Role != inference.RoleSystem && cacheBreakpoint(msg) {
			markLast(out)
		}
	}
	flush()
	return system, systemCached, out
}

// cacheBreakpoint reports whether any block of msg asks for a prompt-cache
// breakpoint. This adapter encodes a message from its text projection rather
// than block by block, so the flag is read from the message as a whole.
func cacheBreakpoint(msg inference.Message) bool {
	for _, block := range msg.ContentBlocks() {
		if block.CacheBreakpoint {
			return true
		}
	}
	return false
}

// markLast puts the breakpoint on the final block of the final message, which
// is where Anthropic reads it: cache_control is a field on a content block and
// caches everything before it.
func markLast(messages []anthropic.MessageParam) {
	if len(messages) == 0 {
		return
	}
	blocks := messages[len(messages)-1].Content
	if len(blocks) == 0 {
		return
	}
	markBlock(&blocks[len(blocks)-1])
}

// markBlock sets cache_control on the block kinds that carry it.
func markBlock(block *anthropic.ContentBlockParamUnion) {
	control := anthropic.NewCacheControlEphemeralParam()
	switch {
	case block.OfText != nil:
		block.OfText.CacheControl = control
	case block.OfImage != nil:
		block.OfImage.CacheControl = control
	case block.OfToolUse != nil:
		block.OfToolUse.CacheControl = control
	case block.OfToolResult != nil:
		block.OfToolResult.CacheControl = control
	}
}

// encodeAssistant emits a text block only when there is text: the API rejects
// empty text blocks, and a tool-calling turn commonly has none.
func encodeAssistant(msg inference.Message) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.ToolCalls)+1)
	if msg.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
	}
	for _, call := range msg.ToolCalls {
		input := strings.TrimSpace(call.Arguments)
		if input == "" || !json.Valid([]byte(input)) {
			input = "{}"
		}
		blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, json.RawMessage(input), call.Name))
	}
	return blocks
}

func encodeTools(tools []inference.Tool) []anthropic.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		schema := anthropic.ToolInputSchemaParam{}
		if props, ok := tool.Parameters["properties"]; ok {
			schema.Properties = props
		}
		if required, ok := tool.Parameters["required"].([]string); ok {
			schema.Required = required
		}
		param := anthropic.ToolParam{Name: tool.Name, InputSchema: schema}
		if tool.Description != "" {
			param.Description = anthropic.String(tool.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &param})
	}
	return out
}

func encodeToolChoice(choice inference.ToolChoice) anthropic.ToolChoiceUnionParam {
	switch choice {
	case inference.ToolChoiceAuto:
		return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
	case inference.ToolChoiceNone:
		return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	case inference.ToolChoiceRequired:
		// The API spells "you must call something" as "any".
		return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
	default:
		return anthropic.ToolChoiceUnionParam{}
	}
}

// decodeStopReason maps the API's stop reasons onto the neutral set, passing
// through anything with no neutral equivalent rather than flattening it.
func decodeStopReason(raw anthropic.StopReason) inference.FinishReason {
	switch raw {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return inference.FinishStop
	case anthropic.StopReasonMaxTokens:
		return inference.FinishLength
	case anthropic.StopReasonToolUse:
		return inference.FinishToolCalls
	case anthropic.StopReasonRefusal:
		return inference.FinishFilter
	case "":
		return inference.FinishUnknown
	default:
		return inference.FinishReason(raw)
	}
}

// decodeUsage sums the three prompt-token fields. InputTokens alone is the
// uncached remainder, not the prompt size — the SDK reports the API's fields
// faithfully, so this correction is the driver's job either way.
func decodeUsage(usage anthropic.Usage) inference.Usage {
	prompt := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	return inference.Usage{
		PromptTokens:       prompt,
		CompletionTokens:   usage.OutputTokens,
		TotalTokens:        prompt + usage.OutputTokens,
		CachedPromptTokens: usage.CacheReadInputTokens,
	}
}

// decodeMessage converts an SDK message into the neutral response. The SDK
// flattens every content-block variant into one struct, so the variant is read
// from Type rather than through a type switch.
func decodeMessage(msg *anthropic.Message) *inference.Response {
	out := &inference.Response{
		ID:           msg.ID,
		Model:        string(msg.Model),
		FinishReason: decodeStopReason(msg.StopReason),
		Usage:        decodeUsage(msg.Usage),
		Message:      inference.Message{Role: inference.RoleAssistant},
	}
	var text, thinking strings.Builder
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			thinking.WriteString(block.Thinking)
		case "tool_use":
			out.Message.ToolCalls = append(out.Message.ToolCalls, inference.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}
	out.Message.Content = text.String()
	out.Message.Reasoning = thinking.String()
	return out
}
