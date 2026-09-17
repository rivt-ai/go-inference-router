package bedrocksdk

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llm "github.com/rivt-ai/go-inference-router"
)

func TestCacheBreakpointsBecomeCachePointBlocks(t *testing.T) {
	input, err := buildInput(llm.Request{
		Model:        "m",
		Instructions: []llm.ContentBlock{llm.Cached(llm.Text("rules"))},
		Messages: []llm.Message{
			llm.UserMessage("hi").Cached(),
			llm.AssistantMessage("", llm.ToolCall{ID: "t1", Name: "w", Arguments: `{}`}),
			llm.ToolMessage("t1", "cold").Cached(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(input.System) != 2 {
		t.Fatalf("system = %#v, want text plus cache point", input.System)
	}
	if _, ok := input.System[1].(*types.SystemContentBlockMemberCachePoint); !ok {
		t.Errorf("system[1] = %T, want a cache point", input.System[1])
	}
	for _, index := range []int{0, 2} {
		content := input.Messages[index].Content
		if _, ok := content[len(content)-1].(*types.ContentBlockMemberCachePoint); !ok {
			t.Errorf("message %d last block = %T, want a cache point", index, content[len(content)-1])
		}
	}
	assistant := input.Messages[1].Content
	if _, ok := assistant[len(assistant)-1].(*types.ContentBlockMemberCachePoint); ok {
		t.Errorf("unmarked assistant message gained a cache point: %#v", assistant)
	}
}

func TestNoCacheBreakpointAddsNoBlocks(t *testing.T) {
	input, err := buildInput(llm.Request{
		Model:        "m",
		Instructions: []llm.ContentBlock{llm.Text("rules")},
		Messages:     []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(input.System) != 1 || len(input.Messages[0].Content) != 1 {
		t.Fatalf("request gained blocks without a breakpoint: %#v", input)
	}
}
