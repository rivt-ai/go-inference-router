// Command tool-calling walks a full tool turn: declare an router.Tool, let the
// model ask for it, run it, and feed the result back with router.ToolMessage
// before asking for the final answer.
//
// ToolCall.Arguments stays raw JSON on purpose — the host owns parsing, so a
// malformed call can be reported back as a tool result (the model is the one
// that can fix it next turn) instead of failing the whole turn.
//
// The fake server plays the model: turn one requests the tool, turn two
// answers.
//
// Run it with:
//
//	go run ./examples/tool-calling
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	router "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

func main() {
	var turn int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		turn++
		if turn == 1 {
			_, _ = io.WriteString(w, `{"id":"c1","model":"m","choices":[{"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_1",
				"function":{"name":"add","arguments":"{\"a\":2,\"b\":3}"}}]}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"c2","model":"m","choices":[{"finish_reason":"stop",
			"message":{"role":"assistant","content":"2 + 3 = 5"}}]}`)
	}))
	defer server.Close()

	client := openaicompat.New(openaicompat.Config{Name: "local", BaseURL: server.URL})
	ctx := context.Background()

	messages := []router.Message{router.UserMessage("What is 2 + 3?")}
	tools := []router.Tool{{
		Name:        "add",
		Description: "Add two numbers.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
			"required": []string{"a", "b"},
		},
	}}

	resp, err := client.Chat(ctx, router.Request{
		Model: "m", Messages: messages, Tools: tools, ToolChoice: router.ToolChoiceAuto,
	})
	if err != nil {
		fmt.Println("chat failed:", err)
		return
	}
	fmt.Println("finish reason:", resp.FinishReason)

	// The assistant turn goes back into the history verbatim, tool calls and
	// all, followed by one tool result message per call.
	messages = append(messages, resp.Message)
	for _, call := range resp.Message.ToolCalls {
		fmt.Printf("model called %s(%s)\n", call.Name, call.Arguments)
		var args struct{ A, B float64 }
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			messages = append(messages, router.ToolMessage(call.ID, "invalid arguments: "+err.Error()))
			continue
		}
		result := fmt.Sprint(args.A + args.B)
		fmt.Printf("tool result: %s\n", result)
		messages = append(messages, router.ToolMessage(call.ID, result))
	}

	final, err := client.Chat(ctx, router.Request{Model: "m", Messages: messages, Tools: tools})
	if err != nil {
		fmt.Println("chat failed:", err)
		return
	}
	fmt.Println("final answer:", final.Message.Content)
}
