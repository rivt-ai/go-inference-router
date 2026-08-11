package llmv1_test

import (
	"encoding/json"
	"testing"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func TestChatRequestWireShape(t *testing.T) {
	data, err := json.Marshal(llmv1.ChatRequest{
		ProfileID: "sonnet",
		Request:   llm.Request{Messages: []llm.Message{llm.UserMessage("hello")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"profile_id":"sonnet","request":{"messages":[{"role":"user","content":"hello","blocks":[{"type":"text","text":"hello"}]}]}}`
	if string(data) != want {
		t.Fatalf("JSON = %s\nwant = %s", data, want)
	}
}

func TestProtocolMethodsAreVersioned(t *testing.T) {
	for _, method := range []string{
		llmv1.MethodInitialize, llmv1.MethodChat, llmv1.MethodInstallAvailable,
		llmv1.MethodInstallRemove, llmv1.MethodProviderInitialize,
	} {
		if len(method) < len(llmv1.Protocol)+1 || method[:len(llmv1.Protocol)+1] != llmv1.Protocol+"." {
			t.Fatalf("unversioned method %q", method)
		}
	}
}

func TestInstallAvailableWireShape(t *testing.T) {
	data, err := json.Marshal(llmv1.InstallAvailableResponse{
		Provider: "openai", InstalledVersions: []string{"v2.0.0-rc.1", "v1.9.9"},
		InstalledVersion: "v1.9.9", AvailableVersion: "v2.0.0", UpdateAvailable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"provider":"openai","installed_versions":["v2.0.0-rc.1","v1.9.9"],"installed_version":"v1.9.9","available_version":"v2.0.0","update_available":true}`
	if string(data) != want {
		t.Fatalf("JSON = %s\nwant = %s", data, want)
	}
}
