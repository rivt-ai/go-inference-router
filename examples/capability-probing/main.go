// Command capability-probing shows how a host discovers what a provider can
// do. Optional behavior lives in small extra interfaces — router.Streamer,
// router.Embedder, router.ModelLister — rather than in one fat Provider full of
// "unsupported" errors. So a host asks with a type assertion and degrades
// gracefully when the answer is no, instead of assuming and failing at
// runtime.
//
// Two providers are probed side by side: the openaicompat driver, which
// implements everything, and a minimal in-process provider that implements
// only the Provider floor.
//
// Run it with:
//
//	go run ./examples/capability-probing
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	router "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

// minimal implements nothing beyond router.Provider.
type minimal struct{}

func (minimal) Name() string { return "minimal" }

func (minimal) Chat(context.Context, router.Request) (*router.Response, error) {
	return &router.Response{Message: router.AssistantMessage("ok"), FinishReason: router.FinishStop}, nil
}

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"local-model","owned_by":"local"}]}`)
	}))
	defer server.Close()

	providers := []router.Provider{
		openaicompat.New(openaicompat.Config{Name: "local", BaseURL: server.URL}),
		minimal{},
	}

	for _, provider := range providers {
		fmt.Printf("provider %q\n", provider.Name())

		if _, ok := provider.(router.Streamer); ok {
			fmt.Println("  streaming:  yes")
		} else {
			fmt.Println("  streaming:  no -> use Chat and render the whole turn at once")
		}

		if _, ok := provider.(router.Embedder); ok {
			fmt.Println("  embeddings: yes")
		} else {
			fmt.Println("  embeddings: no -> route embeddings to another profile")
		}

		lister, ok := provider.(router.ModelLister)
		if !ok {
			fmt.Println("  listing:    no -> use the configured model IDs")
			continue
		}
		models, err := lister.ListModels(context.Background())
		if err != nil {
			fmt.Println("  listing:    failed:", err)
			continue
		}
		fmt.Printf("  listing:    yes %v\n", models)
	}
}
