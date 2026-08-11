// Command custom-provider shows how small the interface floor really is. A
// whole provider is two methods, Name and Chat, which is why an in-process
// model, a test double, or a house-internal gateway can participate without an
// adapter framework or a vendor SDK.
//
// Implementing router.Streamer as well is optional and additive: callers that
// probe for it get incremental output, and callers that do not keep working
// unchanged.
//
// Run it with:
//
//	go run ./examples/custom-provider
package main

import (
	"context"
	"fmt"
	"strings"

	router "github.com/rivt-ai/go-inference-router"
)

// echoProvider is the whole implementation: it uppercases the last message.
type echoProvider struct{}

func (echoProvider) Name() string { return "echo" }

func (echoProvider) Chat(_ context.Context, req router.Request) (*router.Response, error) {
	if len(req.Messages) == 0 {
		// Failures use the same typed error every built-in driver returns, so
		// hosts classify them with router.IsKind like any other provider's.
		return nil, &router.Error{Kind: router.KindInvalidRequest, Provider: "echo", Message: "no messages"}
	}
	last := req.Messages[len(req.Messages)-1]
	return &router.Response{
		Model:        req.Model,
		Message:      router.AssistantMessage(strings.ToUpper(last.Content)),
		FinishReason: router.FinishStop,
		Usage:        router.Usage{CompletionTokens: 1, TotalTokens: 1},
	}, nil
}

// ChatStream is optional: it promotes echoProvider to router.Streamer.
func (p echoProvider) ChatStream(
	ctx context.Context,
	req router.Request,
	onEvent func(router.Event) error,
) (*router.Response, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	for i, word := range strings.Fields(resp.Message.Content) {
		if err := onEvent(router.Event{Kind: router.EventContent, Text: word, Sequence: uint64(i)}); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func main() {
	var provider router.Provider = echoProvider{}
	ctx := context.Background()

	resp, err := provider.Chat(ctx, router.Request{
		Model:    "echo-1",
		Messages: []router.Message{router.UserMessage("hello there")},
	})
	if err != nil {
		fmt.Println("chat failed:", err)
		return
	}
	fmt.Println(provider.Name(), "->", resp.Message.Content)

	if streamer, ok := provider.(router.Streamer); ok {
		_, err := streamer.ChatStream(ctx, router.Request{
			Messages: []router.Message{router.UserMessage("hello there")},
		}, func(event router.Event) error {
			fmt.Printf("event %d: %s\n", event.Sequence, event.Text)
			return nil
		})
		if err != nil {
			fmt.Println("stream failed:", err)
			return
		}
	}

	// Errors from a custom provider classify exactly like a driver's.
	_, err = provider.Chat(ctx, router.Request{})
	fmt.Println("empty request is invalid_request:", router.IsKind(err, router.KindInvalidRequest))
}
