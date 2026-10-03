package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// Decider is the optional capability for non-streaming typed decisions.
type Decider interface {
	Decide(context.Context, DecisionRequest) (*DecisionResponse, error)
}

// DecisionRequest asks independent typed questions about a text or JSON state.
// Model is required for direct provider calls. Router calls select the profile's
// model and reject a different Model. State and instructions must encode a
// string, object, or array; image content blocks are not supported.
type DecisionRequest struct {
	Model     string                      `json:"model,omitempty"`
	State     json.RawMessage             `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
}

// DecisionQuestion contains exactly one question variant.
type DecisionQuestion struct {
	Instructions json.RawMessage `json:"instructions"`
	Choice       *ChoiceQuestion `json:"choice,omitempty"`
	Score        *ScoreQuestion  `json:"score,omitempty"`
	Noul         *NoulQuestion   `json:"noul,omitempty"`
}

// ChoiceQuestion maps option IDs to optional descriptions.
type ChoiceQuestion struct {
	Options map[string]*string `json:"options"`
}

// ScoreQuestion orders level descriptions from lowest to highest.
type ScoreQuestion struct {
	Levels []string `json:"levels"`
}

// NoulQuestion asks for the probability of true, with optional descriptions.
type NoulQuestion struct {
	True  *string `json:"true,omitempty"`
	False *string `json:"false,omitempty"`
}

// DecisionResponse retains the answer for each question and normalized usage.
type DecisionResponse struct {
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   Usage                     `json:"usage"`
}

// DecisionAnswer contains exactly one answer variant, matching its question.
type DecisionAnswer struct {
	Choice *ChoiceAnswer `json:"choice,omitempty"`
	Score  *ScoreAnswer  `json:"score,omitempty"`
	Noul   *NoulAnswer   `json:"noul,omitempty"`
}

// ChoiceAnswer reports the selected option and its distribution.
type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// ScoreAnswer reports the expected level index and its distribution.
type ScoreAnswer struct {
	Score         float64            `json:"score"`
	Legend        map[string]string  `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// NoulAnswer reports the probability that the answer is true.
type NoulAnswer struct {
	Noul float64 `json:"noul"`
}

// Validate checks the portable request shape before executing a decision.
func (r DecisionRequest) Validate() error {
	if err := decisionJSON(r.State); err != nil {
		return &Error{Kind: KindInvalidRequest, Message: "invalid decision state", Err: err}
	}
	if len(r.Questions) == 0 {
		return &Error{Kind: KindInvalidRequest, Message: "decision questions are required"}
	}
	for id, q := range r.Questions {
		if id == "" {
			return &Error{Kind: KindInvalidRequest, Message: "decision question ID is empty"}
		}
		if err := q.validate(); err != nil {
			return &Error{Kind: KindInvalidRequest, Message: fmt.Sprintf("invalid decision question %q", id), Err: err}
		}
	}
	return nil
}

func (q DecisionQuestion) validate() error {
	if err := decisionJSON(q.Instructions); err != nil {
		return fmt.Errorf("instructions: %w", err)
	}
	count := 0
	if q.Choice != nil {
		count++
		if len(q.Choice.Options) < 2 {
			return fmt.Errorf("choice requires at least two options")
		}
		if _, empty := q.Choice.Options[""]; empty {
			return fmt.Errorf("choice option ID is empty")
		}
	}
	if q.Score != nil {
		count++
		if len(q.Score.Levels) < 2 || len(q.Score.Levels) > 10 {
			return fmt.Errorf("score requires 2 to 10 levels")
		}
	}
	if q.Noul != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("exactly one of choice, score, or noul is required")
	}
	return nil
}

func decisionJSON(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) || (raw[0] != '"' && raw[0] != '{' && raw[0] != '[') {
		return fmt.Errorf("expected a JSON string, object, or array")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if decisionImage(value) {
		return fmt.Errorf("image content blocks are unsupported")
	}
	return nil
}

// Image blocks embedded in chat-shaped state must not enable multimodal calls.
func decisionImage(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if v["type"] == "image_url" || v["type"] == "input_image" || v["type"] == "image" {
			return true
		}
		for _, child := range v {
			if decisionImage(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(v, decisionImage)
	}
	return false
}

// UnmarshalJSON rejects unsupported input modes instead of silently dropping
// them at a JSON-RPC boundary. Unknown additive fields remain compatible.
func (r *DecisionRequest) UnmarshalJSON(data []byte) error {
	type request DecisionRequest
	var wire struct {
		request
		Images json.RawMessage `json:"images"`
		Stream bool            `json:"stream"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Images) != 0 || wire.Stream {
		return &Error{Kind: KindInvalidRequest, Message: "decision images and streaming are unsupported"}
	}
	*r = DecisionRequest(wire.request)
	return nil
}
