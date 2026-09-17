package bedrocksdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llm "github.com/rivt-ai/go-inference-router"
)

func buildInput(request llm.Request) (*bedrockruntime.ConverseInput, error) { //nolint:gocognit,gocyclo // request translation is clearest as one pass
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	input := &bedrockruntime.ConverseInput{ModelId: aws.String(request.Model)}
	for _, instruction := range request.Instructions {
		if instruction.Type != llm.ContentText {
			return nil, errors.New("bedrock system instructions must be text")
		}
		input.System = append(input.System, &types.SystemContentBlockMemberText{Value: instruction.Text})
		if instruction.CacheBreakpoint {
			input.System = append(input.System, systemCachePoint())
		}
	}
	for _, message := range request.Messages {
		if message.Role == llm.RoleSystem {
			for _, block := range message.ContentBlocks() {
				if block.Type != llm.ContentText {
					return nil, errors.New("bedrock system messages must be text")
				}
				input.System = append(input.System, &types.SystemContentBlockMemberText{Value: block.Text})
				if block.CacheBreakpoint {
					input.System = append(input.System, systemCachePoint())
				}
			}
			continue
		}
		converted, err := messageBlocks(message)
		if err != nil {
			return nil, err
		}
		role := types.ConversationRoleUser
		if message.Role == llm.RoleAssistant {
			role = types.ConversationRoleAssistant
		}
		input.Messages = append(input.Messages, types.Message{Role: role, Content: converted})
	}
	input.InferenceConfig = inferenceConfig(request)
	tools, err := toolConfig(request)
	if err != nil {
		return nil, err
	}
	input.ToolConfig = tools
	if request.ResponseFormat == llm.FormatJSON {
		return nil, errors.New("bedrock requires a JSON schema for structured output")
	}
	if request.ResponseFormat == llm.FormatSchema {
		schema, err := json.Marshal(request.ResponseSchema)
		if err != nil {
			return nil, err
		}
		name := request.SchemaName
		if name == "" {
			name = "response"
		}
		input.OutputConfig = &types.OutputConfig{TextFormat: &types.OutputFormat{
			Type: types.OutputFormatTypeJsonSchema,
			Structure: &types.OutputFormatStructureMemberJsonSchema{Value: types.JsonSchemaDefinition{
				Name: aws.String(name), Schema: aws.String(string(schema)),
			}},
		}}
	}
	if len(request.Extra) != 0 {
		input.AdditionalModelRequestFields = document.NewLazyDocument(request.Extra)
	}
	return input, nil
}

// systemCachePoint and messageCachePoint mark a prompt-cache breakpoint. Bedrock
// spells one as its own content block rather than as a field on the block it
// follows, so a marked block becomes two entries.
func systemCachePoint() types.SystemContentBlock {
	return &types.SystemContentBlockMemberCachePoint{Value: types.CachePointBlock{Type: types.CachePointTypeDefault}}
}

func messageCachePoint() types.ContentBlock {
	return &types.ContentBlockMemberCachePoint{Value: types.CachePointBlock{Type: types.CachePointTypeDefault}}
}

func messageBlocks(message llm.Message) ([]types.ContentBlock, error) { //nolint:gocognit,gocyclo // each content type maps directly to one SDK type
	if message.Role == llm.RoleTool {
		converted := []types.ContentBlock{&types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
			ToolUseId: aws.String(message.ToolCallID),
			Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: message.Content}},
		}}}
		if cacheBreakpoint(message) {
			converted = append(converted, messageCachePoint())
		}
		return converted, nil
	}
	var converted []types.ContentBlock
	for _, block := range message.ContentBlocks() {
		switch block.Type {
		case llm.ContentText:
			converted = append(converted, &types.ContentBlockMemberText{Value: block.Text})
		case llm.ContentImage:
			if block.Media == nil || len(block.Media.Data) == 0 || block.Media.URI != "" {
				return nil, errors.New("bedrock image input requires inline data")
			}
			format, err := imageFormat(block.Media.MIMEType)
			if err != nil {
				return nil, err
			}
			converted = append(converted, &types.ContentBlockMemberImage{Value: types.ImageBlock{
				Format: format, Source: &types.ImageSourceMemberBytes{Value: block.Media.Data},
			}})
		case llm.ContentToolCall:
			if block.ToolCall == nil {
				return nil, errors.New("empty tool call block")
			}
			var arguments any
			if err := json.Unmarshal([]byte(block.ToolCall.Arguments), &arguments); err != nil {
				return nil, fmt.Errorf("tool call arguments: %w", err)
			}
			converted = append(converted, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: aws.String(block.ToolCall.ID), Name: aws.String(block.ToolCall.Name), Input: document.NewLazyDocument(arguments),
			}})
		case llm.ContentToolResult:
			if block.ToolResult == nil {
				return nil, errors.New("empty tool result block")
			}
			var content []types.ToolResultContentBlock
			for _, result := range block.ToolResult.Content {
				if result.Type != llm.ContentText {
					return nil, errors.New("bedrock tool results currently require text")
				}
				content = append(content, &types.ToolResultContentBlockMemberText{Value: result.Text})
			}
			status := types.ToolResultStatusSuccess
			if block.ToolResult.IsError {
				status = types.ToolResultStatusError
			}
			converted = append(converted, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
				ToolUseId: aws.String(block.ToolResult.ToolCallID), Content: content, Status: status,
			}})
		case llm.ContentReasoning:
			// Reasoning summaries are output metadata and are not replayed without
			// the provider signature Bedrock requires.
		case llm.ContentAudio:
			return nil, errors.New("bedrock audio input is not enabled by this adapter")
		default:
			return nil, fmt.Errorf("unsupported content block %q", block.Type)
		}
		if block.CacheBreakpoint && len(converted) > 0 {
			converted = append(converted, messageCachePoint())
		}
	}
	return converted, nil
}

// cacheBreakpoint reports whether any block of message asks for a breakpoint.
// The tool-result path collapses a message into one block, so the flag is read
// from the message as a whole there.
func cacheBreakpoint(message llm.Message) bool {
	for _, block := range message.ContentBlocks() {
		if block.CacheBreakpoint {
			return true
		}
	}
	return false
}

func inferenceConfig(request llm.Request) *types.InferenceConfiguration {
	config := &types.InferenceConfiguration{StopSequences: request.Stop}
	if request.MaxOutputTokens > 0 {
		value := int32(request.MaxOutputTokens)
		config.MaxTokens = &value
	}
	if request.Temperature != nil {
		value := float32(*request.Temperature)
		config.Temperature = &value
	}
	if request.TopP != nil {
		value := float32(*request.TopP)
		config.TopP = &value
	}
	if config.MaxTokens == nil && config.Temperature == nil && config.TopP == nil && len(config.StopSequences) == 0 {
		return nil
	}
	return config
}

func toolConfig(request llm.Request) (*types.ToolConfiguration, error) {
	if len(request.Tools) == 0 || request.ToolChoice == llm.ToolChoiceNone {
		return nil, nil
	}
	config := &types.ToolConfiguration{}
	for _, tool := range request.Tools {
		config.Tools = append(config.Tools, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name: aws.String(tool.Name), Description: aws.String(tool.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(tool.Parameters)},
		}})
	}
	switch request.ToolChoice {
	case llm.ToolChoiceRequired:
		config.ToolChoice = &types.ToolChoiceMemberAny{Value: types.AnyToolChoice{}}
	case "", llm.ToolChoiceAuto:
		config.ToolChoice = &types.ToolChoiceMemberAuto{Value: types.AutoToolChoice{}}
	default:
		return nil, fmt.Errorf("unsupported tool choice %q", request.ToolChoice)
	}
	return config, nil
}

func imageFormat(mime string) (types.ImageFormat, error) {
	switch strings.ToLower(mime) {
	case "image/png":
		return types.ImageFormatPng, nil
	case "image/jpeg", "image/jpg":
		return types.ImageFormatJpeg, nil
	case "image/gif":
		return types.ImageFormatGif, nil
	case "image/webp":
		return types.ImageFormatWebp, nil
	default:
		return "", fmt.Errorf("unsupported Bedrock image MIME type %q", mime)
	}
}

func decodeResponse(model string, output *bedrockruntime.ConverseOutput) *llm.Response {
	response := &llm.Response{Model: model, FinishReason: finishReason(output.StopReason)}
	message, ok := output.Output.(*types.ConverseOutputMemberMessage)
	if ok {
		response.Message.Role = llm.RoleAssistant
		for _, block := range message.Value.Content {
			switch value := block.(type) {
			case *types.ContentBlockMemberText:
				response.Message.Content += value.Value
				response.Message.Blocks = append(response.Message.Blocks, llm.Text(value.Value))
			case *types.ContentBlockMemberToolUse:
				var input any
				_ = value.Value.Input.UnmarshalSmithyDocument(&input)
				arguments, _ := json.Marshal(input)
				call := llm.ToolCall{ID: aws.ToString(value.Value.ToolUseId), Name: aws.ToString(value.Value.Name), Arguments: string(arguments)}
				response.Message.ToolCalls = append(response.Message.ToolCalls, call)
				response.Message.Blocks = append(response.Message.Blocks, llm.ContentBlock{Type: llm.ContentToolCall, ToolCall: &call})
			case *types.ContentBlockMemberReasoningContent:
				if reasoning, ok := value.Value.(*types.ReasoningContentBlockMemberReasoningText); ok {
					response.Message.Reasoning += aws.ToString(reasoning.Value.Text)
				}
			}
		}
	}
	if output.Usage != nil {
		response.Usage = llm.Usage{
			PromptTokens: int64(aws.ToInt32(output.Usage.InputTokens)), CompletionTokens: int64(aws.ToInt32(output.Usage.OutputTokens)),
			TotalTokens: int64(aws.ToInt32(output.Usage.TotalTokens)), CachedPromptTokens: int64(aws.ToInt32(output.Usage.CacheReadInputTokens)),
		}
	}
	return response
}

func finishReason(reason types.StopReason) llm.FinishReason {
	switch reason {
	case types.StopReasonEndTurn, types.StopReasonStopSequence:
		return llm.FinishStop
	case types.StopReasonToolUse:
		return llm.FinishToolCalls
	case types.StopReasonMaxTokens, types.StopReasonModelContextWindowExceeded:
		return llm.FinishLength
	case types.StopReasonContentFiltered, types.StopReasonGuardrailIntervened:
		return llm.FinishFilter
	default:
		return llm.FinishUnknown
	}
}
