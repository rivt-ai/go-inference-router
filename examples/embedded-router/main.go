// Command embedded-router shows the Router embedded in a host process:
// configuration names Model Profiles, callers select one by ID, and providers
// are opened lazily on first use.
//
// The interesting seam is router.Source. The Router owns profile resolution
// and provider lifetime; the host decides what a Provider Definition actually
// resolves to. Production hosts use router.DefaultSource (in-process
// openai-compatible driver, isolated processes for SDK-backed providers);
// tests and embedders substitute something like the in-memory source below
// without changing routing behavior at all. That is why this example needs no
// network and no installed provider binaries.
//
// The router lives in its own Go module, so this example carries its own
// go.mod with replace directives back to the repo. That means it is run from
// its own directory rather than from the repo root:
//
//	cd examples/embedded-router && go run .
package main

import (
	"context"
	"fmt"

	inference "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/config"
)

// memoryProvider is whatever the host already has: an in-process model, a
// stub, or a corporate gateway client.
type memoryProvider struct{ id string }

func (p memoryProvider) Name() string { return p.id }

func (p memoryProvider) Chat(_ context.Context, req inference.Request) (*inference.Response, error) {
	return &inference.Response{
		Model:        req.Model,
		Message:      inference.AssistantMessage("answered by " + p.id + "/" + req.Model),
		FinishReason: inference.FinishStop,
	}, nil
}

// memorySource is the router.Source implementation: Available reports whether
// a definition could be opened here and now, Open actually opens it.
type memorySource struct{}

func (memorySource) Available(_ context.Context, _ string, _ config.Provider) bool { return true }

func (memorySource) Open(_ context.Context, id string, _ config.Provider) (inference.Provider, error) {
	return memoryProvider{id: id}, nil
}

func main() {
	cfg := config.Config{
		Version: config.Version,
		Providers: map[string]config.Provider{
			"local": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:8080"},
		},
		Models: map[string]config.ModelProfile{
			"fast":  {Provider: "local", Model: "small-model"},
			"smart": {Provider: "local", Model: "big-model"},
		},
	}

	// The third argument is an inference.Observer; nil disables observations.
	r, err := router.New(cfg, memorySource{}, nil)
	if err != nil {
		fmt.Println("router:", err)
		return
	}
	defer func() { _ = r.Close() }()

	ctx := context.Background()
	for _, profile := range r.Profiles(ctx) {
		fmt.Printf("profile %s -> %s/%s available=%t\n",
			profile.ID, profile.Provider, profile.Model, profile.Available)
	}

	// A nil onEvent asks for a non-streaming turn; pass a handler to stream.
	resp, err := r.Chat(ctx, "smart", inference.Request{
		Messages: []inference.Message{inference.UserMessage("hello")},
	}, nil)
	if err != nil {
		fmt.Println("chat:", err)
		return
	}
	fmt.Println(resp.Message.Content)

	// Profile IDs are the host-facing name; an unknown one is a typed error.
	_, err = r.Chat(ctx, "missing", inference.Request{}, nil)
	fmt.Println("unknown profile is invalid_request:", inference.IsKind(err, inference.KindInvalidRequest))
}
