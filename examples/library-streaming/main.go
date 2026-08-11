// Command library-streaming shows that streaming does not change the result
// contract. ChatStream hands each delta to onEvent as it arrives, and the
// *router.Response it returns at the end is the accumulated turn — the same shape
// Chat would have produced for the same conversation. That means a host can
// switch a call site between streaming and non-streaming without reworking how
// it consumes the answer.
//
// The provider is a fake SSE server started inline, so there is no network.
//
// Run it with:
//
//	go run ./examples/library-streaming
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
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"id":"c1","model":"local-model","choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"content":" there"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		} {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := openaicompat.New(openaicompat.Config{Name: "local", BaseURL: server.URL})

	resp, err := client.ChatStream(context.Background(), router.Request{
		Model:    "local-model",
		Messages: []router.Message{router.UserMessage("Say hello.")},
	}, func(event router.Event) error {
		// Returning an error here aborts the stream and surfaces to the caller.
		if event.Kind == router.EventContent {
			fmt.Printf("delta: %q\n", event.Text)
		}
		return nil
	})
	if err != nil {
		fmt.Println("stream failed:", err)
		return
	}

	// Identical to a non-streaming Chat result: message, finish reason, usage.
	fmt.Println("accumulated:   ", resp.Message.Content)
	fmt.Println("finish reason: ", resp.FinishReason)
	fmt.Println("total tokens:  ", resp.Usage.TotalTokens)
}
