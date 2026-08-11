// Command library-chat shows the smallest useful adoption of the library: one
// openaicompat client, one Chat call. In a real program BaseURL points at
// OpenAI, llama.cpp, vLLM, Ollama, OpenRouter, or anything else speaking the
// OpenAI chat-completions wire format. Here it points at a canned httptest
// server started inline, so the program is hermetic: no network, no API key.
//
// Run it with:
//
//	go run ./examples/library-chat
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
	// Stand-in for a real endpoint: the driver only speaks the wire format, so
	// a canned response exercises the same code path a live provider would.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c1","model":"local-model","choices":[
			{"finish_reason":"stop","message":{"role":"assistant","content":"Hello from the model."}}],
			"usage":{"prompt_tokens":9,"completion_tokens":5,"total_tokens":14}}`)
	}))
	defer server.Close()

	client := openaicompat.New(openaicompat.Config{
		Name:    "local",
		BaseURL: server.URL,
		APIKey:  "not-used-by-this-server",
	})

	resp, err := client.Chat(context.Background(), router.Request{
		Model:        "local-model",
		Instructions: []router.ContentBlock{router.Text("Be brief.")},
		Messages:     []router.Message{router.UserMessage("Say hello.")},
	})
	if err != nil {
		fmt.Println("chat failed:", err)
		return
	}

	fmt.Println("provider:      ", client.Name())
	fmt.Println("content:       ", resp.Message.Content)
	fmt.Println("finish reason: ", resp.FinishReason)
	fmt.Println("total tokens:  ", resp.Usage.TotalTokens)
}
