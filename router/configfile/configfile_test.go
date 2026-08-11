package configfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivt-ai/go-inference-router/router/configfile"
)

func TestWorkspaceDefinitionsReplaceUserDefinitions(t *testing.T) {
	dir := t.TempDir()
	user := write(t, dir, "user.yaml", `
version: 1
providers:
  openai:
    type: openai
    secrets:
      api_key: {env: OPENAI_API_KEY}
models:
  gpt:
    provider: openai
    model: gpt-5
`)
	workspace := write(t, dir, "workspace.yaml", `
version: 1
providers:
  local:
    type: openai-compatible
    base_url: http://localhost:8080/v1
models:
  gpt:
    provider: local
    model: qwen-coder
`)

	cfg, err := configfile.Load(user, workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models["gpt"].Provider != "local" || cfg.Models["gpt"].Model != "qwen-coder" {
		t.Fatalf("profile not replaced: %#v", cfg.Models["gpt"])
	}
	if _, ok := cfg.Providers["openai"]; !ok {
		t.Fatal("unshadowed user provider missing")
	}
}

func TestPlaintextSecretAndUnknownFieldsAreRejected(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"plain":    "version: 1\nproviders:\n  p:\n    type: openai\n    secrets:\n      api_key: plaintext\nmodels: {}\n",
		"unknown":  "version: 1\nproviders: {}\nmodels: {}\nsurprise: true\n",
		"reserved": "version: 1\nproviders:\n  p:\n    type: openai\n    options:\n      base_url: hidden\nmodels: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := configfile.Load(write(t, dir, name+".yaml", body), "", ""); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlaintextSecretIsNamedWithoutEchoingItsValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yaml")
	body := "version: 1\nproviders:\n  p:\n    type: openai\n    secrets:\n      api_key: sk-supersecret\nmodels: {}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := configfile.Load(path, "", "")
	if err == nil {
		t.Fatal("plaintext secret was accepted")
	}
	if !strings.Contains(err.Error(), "plaintext") || !strings.Contains(err.Error(), `"p"`) {
		t.Fatalf("error does not name the problem: %v", err)
	}
	if strings.Contains(err.Error(), "sk-supersecret") {
		t.Fatalf("error echoed the secret value: %v", err)
	}
}
