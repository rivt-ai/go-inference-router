package inference

// ToolChoice controls whether a model may call tools.
type ToolChoice string

const (
	// ToolChoiceAuto lets the model decide whether to call a tool.
	ToolChoiceAuto ToolChoice = "auto"
	// ToolChoiceNone disables tool calls.
	ToolChoiceNone ToolChoice = "none"
	// ToolChoiceRequired requires at least one tool call.
	ToolChoiceRequired ToolChoice = "required"
)

// ResponseFormat selects plain text or structured output.
type ResponseFormat string

const (
	// FormatText requests ordinary model text.
	FormatText ResponseFormat = ""
	// FormatJSON requests a valid JSON object.
	FormatJSON ResponseFormat = "json_object"
	// FormatSchema requests JSON conforming to ResponseSchema.
	FormatSchema ResponseFormat = "json_schema"
)

// Request is a provider-neutral model request. Zero-valued optional fields
// leave provider defaults in force.
type Request struct {
	Model        string         `json:"model,omitempty"`
	Instructions []ContentBlock `json:"instructions,omitempty"`
	Messages     []Message      `json:"messages,omitempty"`
	Tools        []Tool         `json:"tools,omitempty"`

	ToolChoice ToolChoice `json:"tool_choice,omitempty"`
	// ParallelToolCalls is a pointer because the provider default is true: an
	// explicit false must stay distinguishable from "not set", which a bool
	// with omitempty would erase.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	ResponseFormat ResponseFormat `json:"response_format,omitempty"`
	ResponseSchema map[string]any `json:"response_schema,omitempty"`
	SchemaName     string         `json:"schema_name,omitempty"`

	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Stop            []string       `json:"stop,omitempty"`
	Seed            *int64         `json:"seed,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
}

// EmbeddingRequest describes a batch of texts to embed.
type EmbeddingRequest struct {
	Model      string   `json:"model,omitempty"`
	Texts      []string `json:"texts"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// Float returns a pointer to v for optional request fields.
func Float(v float64) *float64 { return &v }

// Int64 returns a pointer to v for optional request fields.
func Int64(v int64) *int64 { return &v }

// Bool returns a pointer to v for optional request fields.
func Bool(v bool) *bool { return &v }
