package inference

// Role identifies who authored a conversation message.
type Role string

const (
	// RoleSystem identifies model-wide instructions.
	RoleSystem Role = "system"
	// RoleUser identifies end-user input.
	RoleUser Role = "user"
	// RoleAssistant identifies model output.
	RoleAssistant Role = "assistant"
	// RoleTool identifies a tool result.
	RoleTool Role = "tool"
)

// ContentType identifies a typed message block.
type ContentType string

const (
	// ContentText identifies a plain text block.
	ContentText ContentType = "text"
	// ContentImage identifies image input.
	ContentImage ContentType = "image"
	// ContentAudio identifies audio input.
	ContentAudio ContentType = "audio"
	// ContentToolCall identifies a model tool request.
	ContentToolCall ContentType = "tool_call"
	// ContentToolResult identifies a tool response.
	ContentToolResult ContentType = "tool_result"
	// ContentReasoning identifies provider-exposed reasoning metadata.
	ContentReasoning ContentType = "reasoning"
)

// Media is an inline or referenced input. Exactly one of URI and Data should
// be set. Data is base64-encoded by encoding/json.
type Media struct {
	MIMEType string `json:"mime_type,omitempty"`
	URI      string `json:"uri,omitempty"`
	Data     []byte `json:"data,omitempty"`
}

// ToolResult is the host's response to one model tool call.
type ToolResult struct {
	ToolCallID string         `json:"tool_call_id"`
	Content    []ContentBlock `json:"content,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
}

// Reasoning carries provider-exposed reasoning metadata. Summary is safe for
// display; provider-specific continuation data belongs in Request.Extra.
type Reasoning struct {
	Summary string `json:"summary,omitempty"`
	Tokens  int64  `json:"tokens,omitempty"`
}

// ContentBlock is one part of a multimodal message. The field matching Type
// is populated; unknown future types can be ignored by older callers.
type ContentBlock struct {
	Type       ContentType `json:"type"`
	Text       string      `json:"text,omitempty"`
	Media      *Media      `json:"media,omitempty"`
	ToolCall   *ToolCall   `json:"tool_call,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
	Reasoning  *Reasoning  `json:"reasoning,omitempty"`
}

// Text creates a plain text content block.
func Text(content string) ContentBlock {
	return ContentBlock{Type: ContentText, Text: content}
}

// ImageURL creates an image block referencing a URI.
func ImageURL(mimeType, uri string) ContentBlock {
	return ContentBlock{Type: ContentImage, Media: &Media{MIMEType: mimeType, URI: uri}}
}

// ImageData creates an image block containing inline bytes.
func ImageData(mimeType string, data []byte) ContentBlock {
	return ContentBlock{Type: ContentImage, Media: &Media{MIMEType: mimeType, Data: data}}
}

// AudioURL creates an audio block referencing a URI.
func AudioURL(mimeType, uri string) ContentBlock {
	return ContentBlock{Type: ContentAudio, Media: &Media{MIMEType: mimeType, URI: uri}}
}

// AudioData creates an audio block containing inline bytes.
func AudioData(mimeType string, data []byte) ContentBlock {
	return ContentBlock{Type: ContentAudio, Media: &Media{MIMEType: mimeType, Data: data}}
}

// Message is one conversation turn. Content and the tool fields remain the
// canonical text projection used by in-process adapters; Blocks carries the
// full multimodal representation across llm.v1.
type Message struct {
	Role       Role           `json:"role"`
	Content    string         `json:"content,omitempty"`
	Blocks     []ContentBlock `json:"blocks,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
}

// SystemMessage creates a system-authored text message.
func SystemMessage(content string) Message { return message(RoleSystem, content) }

// UserMessage creates a user-authored message with optional typed blocks.
func UserMessage(content string, extra ...ContentBlock) Message {
	msg := message(RoleUser, content)
	msg.Blocks = append(msg.Blocks, extra...)
	return msg
}

// AssistantMessage creates an assistant message with optional tool calls.
func AssistantMessage(content string, calls ...ToolCall) Message {
	msg := message(RoleAssistant, content)
	msg.ToolCalls = calls
	for i := range calls {
		call := calls[i]
		msg.Blocks = append(msg.Blocks, ContentBlock{Type: ContentToolCall, ToolCall: &call})
	}
	return msg
}

// ToolMessage creates a result message for one tool call.
func ToolMessage(toolCallID, content string) Message {
	msg := message(RoleTool, content)
	msg.ToolCallID = toolCallID
	msg.Blocks = []ContentBlock{{
		Type:       ContentToolResult,
		ToolResult: &ToolResult{ToolCallID: toolCallID, Content: []ContentBlock{Text(content)}},
	}}
	return msg
}

func message(role Role, content string) Message {
	msg := Message{Role: role, Content: content}
	if content != "" {
		msg.Blocks = []ContentBlock{Text(content)}
	}
	return msg
}

// ContentBlocks returns the typed representation of m, projecting legacy
// text/tool fields when a provider created a Message without Blocks.
func (m Message) ContentBlocks() []ContentBlock {
	if len(m.Blocks) != 0 {
		return m.Blocks
	}
	var blocks []ContentBlock
	if m.Content != "" {
		blocks = append(blocks, Text(m.Content))
	}
	if m.Reasoning != "" {
		blocks = append(blocks, ContentBlock{Type: ContentReasoning, Reasoning: &Reasoning{Summary: m.Reasoning}})
	}
	for i := range m.ToolCalls {
		call := m.ToolCalls[i]
		blocks = append(blocks, ContentBlock{Type: ContentToolCall, ToolCall: &call})
	}
	if m.Role == RoleTool && m.ToolCallID != "" {
		blocks = append(blocks, ContentBlock{Type: ContentToolResult, ToolResult: &ToolResult{
			ToolCallID: m.ToolCallID,
			Content:    []ContentBlock{Text(m.Content)},
		}})
	}
	return blocks
}

// ToolCall is a model request to invoke a tool. Arguments is raw JSON owned by
// the host so malformed model output can be returned as a tool result.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool declares a callable function to the model.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}
