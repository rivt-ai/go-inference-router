package inference

// FinishReason explains why generation ended.
type FinishReason string

const (
	// FinishUnknown indicates an unreported reason.
	FinishUnknown FinishReason = ""
	// FinishStop indicates a natural or requested stop.
	FinishStop FinishReason = "stop"
	// FinishLength indicates the output limit was reached.
	FinishLength FinishReason = "length"
	// FinishToolCalls indicates the model requested tools.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishFilter indicates a provider safety filter stopped generation.
	FinishFilter FinishReason = "content_filter"
)

// Response is a completed provider-neutral generation.
type Response struct {
	ID           string         `json:"id,omitempty"`
	Model        string         `json:"model,omitempty"`
	Message      Message        `json:"message"`
	FinishReason FinishReason   `json:"finish_reason,omitempty"`
	Usage        Usage          `json:"usage,omitempty"`
	Reasoning    Reasoning      `json:"reasoning,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

// Usage reports normalized token accounting.
type Usage struct {
	PromptTokens       int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens   int64 `json:"completion_tokens,omitempty"`
	TotalTokens        int64 `json:"total_tokens,omitempty"`
	CachedPromptTokens int64 `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    int64 `json:"reasoning_tokens,omitempty"`
}

// EventKind identifies a streaming delta type.
type EventKind string

const (
	// EventContent carries visible response text.
	EventContent EventKind = "content"
	// EventReasoning carries provider-exposed reasoning metadata.
	EventReasoning EventKind = "reasoning"
	// EventToolCall carries a tool-call delta.
	EventToolCall EventKind = "tool_call"
)

// Event is one ordered streaming response delta.
type Event struct {
	Kind     EventKind `json:"kind"`
	Text     string    `json:"text,omitempty"`
	ToolCall ToolCall  `json:"tool_call,omitempty"`
	Sequence uint64    `json:"sequence,omitempty"`
}

// Modality identifies a model input type.
type Modality string

const (
	// ModalityText identifies text input.
	ModalityText Modality = "text"
	// ModalityImage identifies image input.
	ModalityImage Modality = "image"
	// ModalityAudio identifies audio input.
	ModalityAudio Modality = "audio"
)

// Capabilities is negotiated per Provider Process and may be narrowed by an
// individual model's Metadata.
type Capabilities struct {
	Streaming        bool       `json:"streaming,omitempty"`
	Tools            bool       `json:"tools,omitempty"`
	StructuredOutput bool       `json:"structured_output,omitempty"`
	Reasoning        bool       `json:"reasoning,omitempty"`
	Embeddings       bool       `json:"embeddings,omitempty"`
	InputModalities  []Modality `json:"input_modalities,omitempty"`
	MaxConcurrency   int        `json:"max_concurrency,omitempty"`
}

// Supports reports whether a modality is accepted as input.
func (c Capabilities) Supports(modality Modality) bool {
	for _, supported := range c.InputModalities {
		if supported == modality {
			return true
		}
	}
	return false
}

// ModelInfo is the stable, provider-neutral model identity.
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// Metadata reports model-specific limits and their source.
type Metadata struct {
	ContextWindowTokens int64  `json:"context_window_tokens,omitempty"`
	MaxOutputTokens     int64  `json:"max_output_tokens,omitempty"`
	Source              string `json:"source,omitempty"`
}
