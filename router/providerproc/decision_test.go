package providerproc_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/providerproc"
	"github.com/rivt-ai/go-inference-router/router/rpcserver"
)

func (helperProvider) Decide(ctx context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	if req.Model == "blocked" {
		<-ctx.Done()
		return nil, &llm.Error{Kind: llm.KindCanceled, Message: "canceled"}
	}
	return &llm.DecisionResponse{Answers: map[string]llm.DecisionAnswer{"q": {Noul: &llm.NoulAnswer{Noul: 0}}}, Usage: llm.Usage{PromptTokens: 7, TotalTokens: 7}}, nil
}

type decisionSource struct{ client *providerproc.Client }

func (decisionSource) Available(context.Context, string, config.Provider) bool { return true }
func (s decisionSource) Open(context.Context, string, config.Provider) (llm.Provider, error) {
	return s.client, nil
}

func TestDecideAcrossRouterAndProviderProcess(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "supported", false: "old-process"}[enabled], func(t *testing.T) {
			env := append(os.Environ(), "INFROUTER_PROVIDER_HELPER=1", "INFROUTER_DECISIONS=0")
			if enabled {
				env = append(env, "INFROUTER_DECISIONS=1")
			}
			client, err := providerproc.Start(context.Background(), providerproc.Options{
				Path: os.Args[0], Args: []string{"-test.run=TestProviderHelperProcess"}, Env: env,
				Initialize: llmv1.ProviderInitializeRequest{ProviderID: "p", Protocols: []string{llmv1.Protocol}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			cfg := config.Config{Version: 1, Providers: map[string]config.Provider{"p": {Type: "helper"}},
				Models: map[string]config.ModelProfile{"decision": {Provider: "p", Model: "helper-model", Decisions: true}, "blocked": {Provider: "p", Model: "blocked", Decisions: true}}}
			r, err := router.New(cfg, decisionSource{client}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			left, right := net.Pipe()
			defer func() { _ = left.Close() }()
			defer func() { _ = right.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := rpcserver.New(right, right)
			go func() { _ = server.Serve(ctx, r, nil, nil) }()
			host := jsonrpc.New(left, left)
			go func() { _ = host.Serve(ctx) }()
			req := llm.DecisionRequest{State: json.RawMessage(`{"state":"text"}`), Questions: map[string]llm.DecisionQuestion{"q": {Instructions: json.RawMessage(`"true?"`), Noul: &llm.NoulQuestion{}}}}
			var result llmv1.DecideResponse
			callCtx, done := context.WithTimeout(ctx, 3*time.Second)
			defer done()
			err = host.Call(callCtx, llmv1.MethodDecide, llmv1.DecideRequest{ProfileID: "decision", Request: req}, &result)
			if !enabled {
				if err == nil {
					t.Fatal("old process accepted decisions")
				}
				_, directErr := client.Decide(ctx, req)
				if !llm.IsKind(directErr, llm.KindInvalidRequest) {
					t.Fatalf("old process: %v", directErr)
				}
				return
			}
			if err != nil || result.Response.Answers["q"].Noul == nil || result.Response.Answers["q"].Noul.Noul != 0 || result.Response.Usage.PromptTokens != 7 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
			defer stop()
			if _, err := r.Decide(short, "blocked", req); err == nil {
				t.Fatal("blocked decision ignored cancellation")
			}
			// A subsequent call proves cancellation released the process slot.
			if _, err := r.Decide(callCtx, "decision", req); err != nil {
				t.Fatal(err)
			}
		})
	}
}
