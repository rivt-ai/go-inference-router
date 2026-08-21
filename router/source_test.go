package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/providerproc"
	"github.com/rivt-ai/go-inference-router/router/secret"
)

type sourceProvider struct{}

func (sourceProvider) Name() string { return "source-test" }
func (sourceProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func TestPathLookupRequiresOptIn(t *testing.T) {
	name := "go-inference-router-provider-custom"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("provider"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(path))
	definition := config.Provider{Type: "custom"}
	if _, ok, err := (DefaultSource{}).path(context.Background(), "custom", definition); err != nil || ok {
		t.Fatalf("default path lookup = %v, %v", ok, err)
	}
	located, ok, err := (DefaultSource{AllowPathLookup: true}).path(context.Background(), "custom", definition)
	if err != nil || !ok || located != path {
		t.Fatalf("opt-in path lookup = %q, %v, %v", located, ok, err)
	}
}

func TestDefaultSourceScopesEnvironmentAndForwardsMaps(t *testing.T) {
	t.Setenv("INFROUTER_TEST_SECRET", "token")
	t.Setenv("INFROUTER_MUST_NOT_REACH_CHILD", "leaked")
	var captured providerproc.Options
	source := DefaultSource{
		Secrets: secret.Resolver{},
		startProcess: func(_ context.Context, options providerproc.Options) (llm.Provider, error) {
			captured = options
			return sourceProvider{}, nil
		},
	}
	_, err := source.Open(context.Background(), "custom", config.Provider{
		Type: "custom", Path: os.Args[0], BaseURL: "https://example.test",
		Options: map[string]any{"region": "us-east-1", "attempts": 2},
		Secrets: map[string]config.SecretRef{"token": {Env: "INFROUTER_TEST_SECRET"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range captured.Env {
		if strings.HasPrefix(entry, "INFROUTER_MUST_NOT_REACH_CHILD=") {
			t.Fatalf("non-allowlisted environment reached child: %q", entry)
		}
	}
	if captured.Initialize.Secrets["token"] != "token" {
		t.Fatalf("secrets = %#v", captured.Initialize.Secrets)
	}
	var region string
	if err := json.Unmarshal(captured.Initialize.Config["region"], &region); err != nil || region != "us-east-1" {
		t.Fatalf("region = %q, %v", region, err)
	}
	if _, nested := captured.Initialize.Config["options"]; nested {
		t.Fatalf("options were not flattened: %#v", captured.Initialize.Config)
	}
}

type countingTransport struct{ calls int }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)),
	}, nil
}

func TestDefaultSourceThreadsTransportIntoInProcessProvider(t *testing.T) {
	transport := &countingTransport{}
	source := DefaultSource{HTTPClient: &http.Client{Transport: transport}, StallTimeout: time.Second}
	provider, err := source.Open(context.Background(), "compat", config.Provider{
		Type: "openai-compatible", BaseURL: "https://example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Chat(context.Background(), llm.Request{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if transport.calls != 1 {
		t.Fatalf("custom transport calls = %d, want 1", transport.calls)
	}
}
