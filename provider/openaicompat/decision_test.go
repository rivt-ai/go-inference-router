package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
)

func decisionRequest() llm.DecisionRequest {
	return llm.DecisionRequest{Model: "decision-model", State: json.RawMessage(`{"message":"hello"}`),
		Questions: map[string]llm.DecisionQuestion{
			"route": {Instructions: json.RawMessage(`"Which?"`), Choice: &llm.ChoiceQuestion{Options: map[string]*string{"a": nil, "b": nil}}},
			"level": {Instructions: json.RawMessage(`["Rate it"]`), Score: &llm.ScoreQuestion{Levels: []string{"low", "high"}}},
			"yes":   {Instructions: json.RawMessage(`{"question":"True?"}`), Noul: &llm.NoulQuestion{}},
		}}
}

const decisionBody = `{"answers":{
"route":{"type":"choice","choice":"a","probabilities":{"a":1,"b":0},"confidence":1},
"level":{"type":"score","score":0,"legend":{"0":"low","1":"high"},"probabilities":{"0":1,"1":0},"confidence":1},
"yes":{"type":"noul","noul":0}
},"usage":{"input_tokens":12,"output_tokens":0}}`

func TestDecideWireAndOptIn(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("X-Test") != "yes" {
			t.Errorf("unexpected request: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var wire struct {
			Model     string          `json:"model"`
			State     json.RawMessage `json:"state"`
			Questions map[string]struct {
				Type     string          `json:"type"`
				Criteria json.RawMessage `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		if wire.Model != "decision-model" || string(wire.State) != `{"message":"hello"}` ||
			wire.Questions["route"].Type != "choice" || string(wire.Questions["route"].Criteria) != `{"a":null,"b":null}` ||
			string(wire.Questions["level"].Criteria) != `["low","high"]` || wire.Questions["yes"].Type != "noul" {
			t.Errorf("wire = %+v", wire)
		}
		_, _ = fmt.Fprint(w, decisionBody)
	}))
	defer server.Close()
	cfg := Config{BaseURL: server.URL + "/v1/", APIKey: "key", Headers: map[string]string{"X-Test": "yes"}, DecisionModels: []string{"decision-model"}}
	client := New(cfg)
	cfg.DecisionModels[0] = "mutated"
	result, err := client.Decide(context.Background(), decisionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Answers["level"].Score.Score != 0 || result.Answers["yes"].Noul.Noul != 0 || result.Usage.PromptTokens != 12 || result.Usage.TotalTokens != 12 {
		t.Fatalf("result = %+v", result)
	}
	for _, model := range []string{"", "other"} {
		req := decisionRequest()
		req.Model = model
		if _, err := client.Decide(context.Background(), req); !llm.IsKind(err, llm.KindInvalidRequest) {
			t.Fatalf("error = %v", err)
		}
		caps, _ := client.Capabilities(context.Background(), model)
		if caps.Decisions {
			t.Fatal("unsupported model advertised")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("disabled model reached backend")
	}
}

func TestDecideMalformedAndUnsupported(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		kind       llm.Kind
	}{
		{"missing answers", `{}`, 200, llm.KindProtocol},
		{"missing zero", `{"answers":{"yes":{"type":"noul"}}}`, 200, llm.KindProtocol},
		{"wrong type", `{"answers":{"yes":{"type":"choice","noul":0}}}`, 200, llm.KindProtocol},
		{"out of range", `{"answers":{"yes":{"type":"noul","noul":2}}}`, 200, llm.KindProtocol},
		{"missing endpoint", `{"error":{"message":"not found"}}`, 404, llm.KindInvalidRequest},
		{"wrong method", `{}`, 405, llm.KindInvalidRequest},
		{"wrong model", `{"error":{"message":"not a decision model"}}`, 501, llm.KindInvalidRequest},
		{"overloaded", `{}`, 503, llm.KindUnavailable},
		{"unauthorized", `{}`, 401, llm.KindAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); _, _ = fmt.Fprint(w, tc.body) }))
			defer server.Close()
			client := New(Config{BaseURL: server.URL, DecisionModels: []string{"decision-model"}})
			req := decisionRequest()
			delete(req.Questions, "route")
			delete(req.Questions, "level")
			_, err := client.Decide(context.Background(), req)
			if !llm.IsKind(err, tc.kind) {
				t.Fatalf("error = %v, want %s", err, tc.kind)
			}
			if tc.kind == llm.KindInvalidRequest && llm.Retryable(err) {
				t.Fatal("unsupported request is retryable")
			}
		})
	}
}

func TestDecideCancellationWhileReadingBody(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"answers":`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, DecisionModels: []string{"decision-model"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Decide(ctx, decisionRequest()); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !llm.IsKind(err, llm.KindCanceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach HTTP request")
	}
}

func TestDecideRejectsMalformedDistributions(t *testing.T) {
	for _, replacement := range []struct{ from, to string }{
		{`"b":0`, `"b":null`},
		{`"b":0`, `"b":0.5`},
		{`"b":0`, `"unknown":0`},
		{`"choice":"a"`, `"choice":"unknown"`},
		{`"score":0,`, ""},
		{`"score":0`, `"score":9`},
		{`"confidence":1`, `"confidence":null`},
		{`"legend":{"0":"low","1":"high"}`, `"legend":{}`},
	} {
		t.Run(replacement.to, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, strings.Replace(decisionBody, replacement.from, replacement.to, 1))
			}))
			defer server.Close()
			client := New(Config{BaseURL: server.URL, DecisionModels: []string{"decision-model"}})
			if _, err := client.Decide(context.Background(), decisionRequest()); !llm.IsKind(err, llm.KindProtocol) {
				t.Fatalf("malformed response accepted: %v", err)
			}
		})
	}
}

func TestDecisionsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, decisionBody)
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, DecisionsOnly: true, AllDecisionModels: true})
	ctx := context.Background()
	caps, _ := client.Capabilities(ctx, "decision-model")
	if !caps.Decisions || caps.Streaming || caps.Tools || caps.StructuredOutput || caps.Embeddings {
		t.Errorf("caps = %+v", caps)
	}
	_, chatErr := client.Chat(ctx, llm.Request{Model: "decision-model"})
	_, streamErr := client.ChatStream(ctx, llm.Request{Model: "decision-model"}, func(llm.Event) error { return nil })
	_, embedErr := client.Embed(ctx, llm.EmbeddingRequest{Model: "decision-model", Texts: []string{"x"}})
	for _, err := range []error{chatErr, streamErr, embedErr} {
		if !llm.IsKind(err, llm.KindInvalidRequest) {
			t.Errorf("err = %v", err)
		}
	}
	if models, err := client.ListModels(ctx); models != nil || err != nil {
		t.Errorf("ListModels = %v, %v", models, err)
	}
	if _, err := client.Decide(ctx, decisionRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestJevConfig(t *testing.T) {
	cfg := JevConfig("key")
	cfg.BaseURL = "http://unused"
	caps, _ := New(cfg).Capabilities(context.Background(), JevModel)
	if !caps.Decisions || caps.Streaming {
		t.Errorf("caps = %+v", caps)
	}
}
