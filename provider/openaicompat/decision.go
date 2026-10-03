package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	inference "github.com/rivt-ai/go-inference-router"
)

type decisionQuestion struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     any             `json:"criteria,omitempty"`
}

type decisionAnswer struct {
	Type          string              `json:"type"`
	Choice        *string             `json:"choice"`
	Score         *float64            `json:"score"`
	Noul          *float64            `json:"noul"`
	Confidence    *float64            `json:"confidence"`
	Probabilities map[string]*float64 `json:"probabilities"`
	Legend        map[string]string   `json:"legend"`
}

// Decide executes a System One request for an explicitly enabled model.
func (c *Client) Decide(ctx context.Context, req inference.DecisionRequest) (*inference.DecisionResponse, error) {
	if !c.decides(req.Model) {
		return nil, c.base.Errf(inference.KindInvalidRequest, 0, "decisions are not enabled for model", nil)
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	questions := make(map[string]decisionQuestion, len(req.Questions))
	for id, q := range req.Questions {
		questions[id] = encodeDecisionQuestion(q)
	}
	payload := struct {
		Model     string                      `json:"model"`
		State     json.RawMessage             `json:"state"`
		Questions map[string]decisionQuestion `json:"questions"`
	}{req.Model, req.State, questions}
	guard := c.base.Guard(ctx)
	defer guard.Stop()
	resp, err := c.post(guard, "/systemone", payload)
	if err != nil {
		return nil, decisionHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Answers map[string]decisionAnswer `json:"answers"`
		Usage   struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		if guard.Stalled() || guard.Context().Err() != nil {
			return nil, c.base.Classify(ctx, err, guard.Stalled())
		}
		return nil, c.base.Errf(inference.KindProtocol, resp.StatusCode, "malformed decision response", err)
	}
	if len(body.Answers) != len(req.Questions) {
		return nil, c.base.Errf(inference.KindProtocol, resp.StatusCode, "decision answer count mismatch", nil)
	}
	result := &inference.DecisionResponse{
		Answers: make(map[string]inference.DecisionAnswer, len(body.Answers)),
		Usage: inference.Usage{PromptTokens: body.Usage.Input, CompletionTokens: body.Usage.Output,
			TotalTokens: body.Usage.Input + body.Usage.Output},
	}
	for id, q := range req.Questions {
		answer, err := body.Answers[id].decode(q)
		if err != nil {
			return nil, c.base.Errf(inference.KindProtocol, resp.StatusCode, fmt.Sprintf("invalid decision answer %q", id), err)
		}
		result.Answers[id] = answer
	}
	return result, nil
}

func encodeDecisionQuestion(q inference.DecisionQuestion) decisionQuestion {
	wire := decisionQuestion{Instructions: q.Instructions}
	switch {
	case q.Choice != nil:
		wire.Type, wire.Criteria = "choice", q.Choice.Options
	case q.Score != nil:
		wire.Type, wire.Criteria = "score", q.Score.Levels
	case q.Noul != nil:
		wire.Type = "noul"
		if q.Noul.True != nil || q.Noul.False != nil {
			wire.Criteria = q.Noul
		}
	}
	return wire
}

func decisionHTTPError(err error) error {
	var failure *inference.Error
	if errors.As(err, &failure) && (failure.Status == 404 || failure.Status == 405 || failure.Status == 501) {
		copy := *failure
		copy.Kind = inference.KindInvalidRequest
		return &copy
	}
	return err
}

func (a decisionAnswer) decode(q inference.DecisionQuestion) (inference.DecisionAnswer, error) {
	if q.Noul != nil {
		if a.Type != "noul" || !probability(a.Noul) {
			return inference.DecisionAnswer{}, fmt.Errorf("missing or invalid noul probability")
		}
		return inference.DecisionAnswer{Noul: &inference.NoulAnswer{Noul: *a.Noul}}, nil
	}
	if !probability(a.Confidence) {
		return inference.DecisionAnswer{}, fmt.Errorf("missing or invalid confidence")
	}
	if q.Choice != nil {
		return a.decodeChoice(q.Choice)
	}
	return a.decodeScore(q.Score)
}

func (a decisionAnswer) decodeChoice(q *inference.ChoiceQuestion) (inference.DecisionAnswer, error) {
	if a.Type != "choice" || a.Choice == nil {
		return inference.DecisionAnswer{}, fmt.Errorf("missing choice")
	}
	if _, ok := q.Options[*a.Choice]; !ok {
		return inference.DecisionAnswer{}, fmt.Errorf("unknown choice")
	}
	keys := make([]string, 0, len(q.Options))
	for key := range q.Options {
		keys = append(keys, key)
	}
	probabilities, err := distribution(a.Probabilities, keys)
	if err != nil {
		return inference.DecisionAnswer{}, err
	}
	return inference.DecisionAnswer{Choice: &inference.ChoiceAnswer{
		Choice: *a.Choice, Probabilities: probabilities, Confidence: *a.Confidence,
	}}, nil
}

func (a decisionAnswer) decodeScore(q *inference.ScoreQuestion) (inference.DecisionAnswer, error) {
	if a.Type != "score" || a.Score == nil || *a.Score < 0 || *a.Score > float64(len(q.Levels)-1) {
		return inference.DecisionAnswer{}, fmt.Errorf("missing or invalid score")
	}
	keys := make([]string, len(q.Levels))
	for i := range q.Levels {
		keys[i] = strconv.Itoa(i)
	}
	probabilities, err := distribution(a.Probabilities, keys)
	if err != nil {
		return inference.DecisionAnswer{}, err
	}
	if len(a.Legend) != len(keys) {
		return inference.DecisionAnswer{}, fmt.Errorf("invalid score legend")
	}
	for i, key := range keys {
		if a.Legend[key] != q.Levels[i] {
			return inference.DecisionAnswer{}, fmt.Errorf("score legend does not match levels")
		}
	}
	return inference.DecisionAnswer{Score: &inference.ScoreAnswer{
		Score: *a.Score, Legend: a.Legend, Probabilities: probabilities, Confidence: *a.Confidence,
	}}, nil
}

func probability(p *float64) bool { return p != nil && *p >= 0 && *p <= 1 }

func distribution(values map[string]*float64, keys []string) (map[string]float64, error) {
	if len(values) != len(keys) {
		return nil, fmt.Errorf("probability keys do not match criteria")
	}
	result := make(map[string]float64, len(keys))
	var sum float64
	for _, key := range keys {
		p := values[key]
		if !probability(p) {
			return nil, fmt.Errorf("missing or invalid probability")
		}
		result[key] = *p
		sum += *p
	}
	if math.Abs(sum-1) > 0.01 {
		return nil, fmt.Errorf("probabilities do not sum to one")
	}
	return result, nil
}
