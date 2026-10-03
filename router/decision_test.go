package router_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/config"
)

func TestDecisionProfilesAndReload(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "first" && body.Model != "second" {
			t.Errorf("model = %s", body.Model)
		}
		_, _ = fmt.Fprint(w, `{"answers":{"q":{"type":"noul","noul":0}},"usage":{"input_tokens":3}}`)
	}))
	defer server.Close()
	cfg := config.Config{Version: 1,
		Providers: map[string]config.Provider{"p": {Type: "openai-compatible", BaseURL: server.URL}},
		Models: map[string]config.ModelProfile{
			"enabled":  {Provider: "p", Model: "first", Decisions: true},
			"disabled": {Provider: "p", Model: "first"},
		}}
	var usage llm.Usage
	r, err := router.New(cfg, router.DefaultSource{}, llm.ObserverFunc(func(_ context.Context, o llm.Observation) {
		if o.Operation == llm.ObservationDecide && o.Phase == llm.ObservationFinished && o.Err == nil {
			usage = o.Usage
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	req := llm.DecisionRequest{State: json.RawMessage(`"state"`), Questions: map[string]llm.DecisionQuestion{"q": {Instructions: json.RawMessage(`"true?"`), Noul: &llm.NoulQuestion{}}}}
	if _, err := r.Decide(context.Background(), "disabled", req); !llm.IsKind(err, llm.KindInvalidRequest) {
		t.Fatalf("disabled: %v", err)
	}
	req.Model = "second"
	if _, err := r.Decide(context.Background(), "enabled", req); !llm.IsKind(err, llm.KindInvalidRequest) {
		t.Fatalf("override: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected requests reached backend")
	}
	req.Model = ""
	if _, err := r.Decide(context.Background(), "enabled", req); err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	for _, id := range []string{"enabled", "disabled"} {
		caps, _, err := r.Capabilities(context.Background(), id)
		if err != nil || caps.Decisions != (id == "enabled") {
			t.Fatalf("%s caps=%+v err=%v", id, caps, err)
		}
	}
	cfg.Models["enabled"] = config.ModelProfile{Provider: "p", Model: "second", Decisions: true}
	if err := r.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Decide(context.Background(), "enabled", req); err != nil {
		t.Fatal(err)
	}
	cfg.Models["enabled"] = config.ModelProfile{Provider: "p", Model: "second"}
	if err := r.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Decide(context.Background(), "enabled", req); !llm.IsKind(err, llm.KindInvalidRequest) {
		t.Fatalf("revoked: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}
