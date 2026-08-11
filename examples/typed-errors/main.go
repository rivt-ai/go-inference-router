// Command typed-errors shows the point of router.Error: every driver maps its
// provider's failures onto a small portable set of router.Kind values, so a host
// branches on the classification instead of pattern-matching error strings
// that differ per vendor and change without notice. One retry policy then
// works across every provider.
//
// The fake server returns HTTP 429, which the driver classifies as
// router.KindRateLimit.
//
// Run it with:
//
//	go run ./examples/typed-errors
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

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_exceeded"}}`)
	}))
	defer server.Close()

	client := openaicompat.New(openaicompat.Config{Name: "local", BaseURL: server.URL})

	_, err := client.Chat(context.Background(), router.Request{
		Model:    "local-model",
		Messages: []router.Message{router.UserMessage("Say hello.")},
	})

	fmt.Println("error:        ", err)
	fmt.Println("kind:         ", router.KindOf(err))
	fmt.Println("rate limited: ", router.IsKind(err, router.KindRateLimit))
	fmt.Println("auth problem: ", router.IsKind(err, router.KindAuth))
	fmt.Println("retryable:    ", router.Retryable(err))

	// A host's whole retry decision: no string matching, no provider special
	// cases. Back off and try again, or surface the failure to the caller.
	if router.Retryable(err) {
		fmt.Println("=> back off and retry the same request")
	} else {
		fmt.Println("=> the request must change before retrying")
	}
}
