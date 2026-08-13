package router

import (
	"testing"

	"github.com/rivt-ai/go-inference-router/router/config"
)

func TestPathLookupRequiresOptInWithTrustedRegistry(t *testing.T) {
	previous := registryPublicKeys
	registryPublicKeys = "compiled-key"
	t.Cleanup(func() { registryPublicKeys = previous })
	if PathLookupAllowed(config.Config{}) {
		t.Fatal("compiled registry key allowed unmanaged PATH providers")
	}
	if !PathLookupAllowed(config.Config{Registry: config.Registry{AllowPathLookup: true}}) {
		t.Fatal("explicit PATH lookup opt-in was ignored")
	}
}

func TestPathLookupAllowedWithoutAnyTrustRoot(t *testing.T) {
	previous := registryPublicKeys
	registryPublicKeys = ""
	t.Cleanup(func() { registryPublicKeys = previous })
	if !PathLookupAllowed(config.Config{}) {
		t.Fatal("development build without a trust root refused PATH providers")
	}
}

func TestOpenWithSuppliedConfigReadsNoFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent-on-purpose")
	cfg := config.Config{
		Version:   config.Version,
		Providers: map[string]config.Provider{"local": {Type: "openai-compatible"}},
		Models:    map[string]config.ModelProfile{"small": {Provider: "local", Model: "m"}},
	}
	r, err := Open(t.Context(), Options{Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if profiles := r.Profiles(t.Context()); len(profiles) != 1 || profiles[0].ID != "small" {
		t.Fatalf("profiles = %+v", profiles)
	}
	if _, err := r.Reload(t.Context()); err == nil {
		t.Fatal("an in-memory configuration reported a reload path")
	}
}

func TestOpenRejectsInvalidSuppliedConfig(t *testing.T) {
	cfg := config.Config{Version: config.Version, Models: map[string]config.ModelProfile{
		"orphan": {Provider: "missing", Model: "m"},
	}}
	if _, err := Open(t.Context(), Options{Config: &cfg}); err == nil {
		t.Fatal("a profile referencing an unknown provider was accepted")
	}
}

func TestSecretsRequiredWhenConfigReferencesThem(t *testing.T) {
	source := DefaultSource{}
	_, err := source.Open(t.Context(), "p", config.Provider{
		Type: "openai-compatible", Secrets: map[string]config.SecretRef{"api_key": {Env: "X"}},
	})
	if err == nil {
		t.Fatal("secret references resolved without a resolver")
	}
}
