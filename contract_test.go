package inference_test

import (
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
)

func TestMessageCarriesTypedContent(t *testing.T) {
	msg := llm.UserMessage("describe this", llm.ImageURL("image/png", "https://example.test/image.png"))
	if msg.Content != "describe this" {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.Blocks) != 2 || msg.Blocks[0].Type != llm.ContentText || msg.Blocks[1].Type != llm.ContentImage {
		t.Fatalf("blocks = %#v", msg.Blocks)
	}
	if msg.Blocks[1].Media.URI != "https://example.test/image.png" {
		t.Fatalf("image URI = %q", msg.Blocks[1].Media.URI)
	}
}

func TestCapabilitiesReportModalities(t *testing.T) {
	caps := llm.Capabilities{Streaming: true, InputModalities: []llm.Modality{llm.ModalityText, llm.ModalityImage}}
	if !caps.Supports(llm.ModalityImage) || caps.Supports(llm.ModalityAudio) {
		t.Fatalf("unexpected modalities: %#v", caps.InputModalities)
	}
}

func TestCachedMarksTheLastBlock(t *testing.T) {
	original := llm.SystemMessage("rules")
	marked := original.Cached()
	if blocks := marked.Blocks; len(blocks) != 1 || !blocks[0].CacheBreakpoint {
		t.Fatalf("blocks = %#v, want one block marked", marked.Blocks)
	}
	if original.Blocks[0].CacheBreakpoint {
		t.Error("Cached mutated the message it was called on")
	}
	if empty := (llm.Message{Role: llm.RoleUser}).Cached(); len(empty.Blocks) != 0 {
		t.Errorf("empty message gained blocks: %#v", empty)
	}
}
