package router

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/install"
)

func TestPathLookupRequiresOptInWithTrustedRegistry(t *testing.T) {
	previous := registryPublicKey
	registryPublicKey = "compiled-key"
	t.Cleanup(func() { registryPublicKey = previous })
	if PathLookupAllowed(config.Config{}) {
		t.Fatal("compiled registry key allowed unmanaged PATH providers")
	}
	if !PathLookupAllowed(config.Config{Registry: config.Registry{AllowPathLookup: true}}) {
		t.Fatal("explicit PATH lookup opt-in was ignored")
	}
}

func TestPathLookupAllowedWithoutAnyTrustRoot(t *testing.T) {
	previous := registryPublicKey
	registryPublicKey = ""
	t.Cleanup(func() { registryPublicKey = previous })
	if !PathLookupAllowed(config.Config{}) {
		t.Fatal("development build without a trust root refused PATH providers")
	}
}

// A host can establish a registry trust root without any compiled-in key. When
// it does, unmanaged PATH providers must stay refused: judging the trust root
// from the key alone would silently re-enable exactly the unsigned binaries the
// host opted in to verifying.
func TestPathLookupRefusedWhenInstallerExistsWithoutCompiledKey(t *testing.T) {
	previous := registryPublicKey
	registryPublicKey = ""
	t.Cleanup(func() { registryPublicKey = previous })
	key, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	installer := install.New("https://example.invalid/registry.json", install.NewKeyVerifier(key), t.TempDir(), nil, nil)
	if pathLookupAllowed(config.Config{}, installer) {
		t.Fatal("a configured trust root without a compiled-in key allowed unmanaged PATH providers")
	}
	if !pathLookupAllowed(config.Config{Registry: config.Registry{AllowPathLookup: true}}, installer) {
		t.Fatal("explicit PATH lookup opt-in was ignored")
	}
	if !pathLookupAllowed(config.Config{}, nil) {
		t.Fatal("a build with no trust root at all refused PATH providers")
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

func TestSourceBuildsCarryTheReleaseTrustRoot(t *testing.T) {
	if registryPublicKey != ReleasePublicKey {
		t.Fatalf("source default = %q, want ReleasePublicKey", registryPublicKey)
	}
	decoded, err := base64.StdEncoding.DecodeString(ReleasePublicKey)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		t.Fatalf("ReleasePublicKey must be base64 Ed25519: %v", err)
	}
	installer, err := NewInstaller(config.Config{}, nil, nil)
	if err != nil || installer == nil {
		t.Fatalf("default build must have an installer: %v, %v", installer, err)
	}
}

func TestRequireInstallerIsLoudWithoutTrustRoot(t *testing.T) {
	r := &Router{}
	if _, err := r.RequireInstaller(); !errors.Is(err, ErrNoTrustRoot) {
		t.Fatalf("err = %v, want ErrNoTrustRoot", err)
	}
	r.installer = install.New("https://example.invalid/r.json", nil, t.TempDir(), nil, nil)
	if got, err := r.RequireInstaller(); err != nil || got == nil {
		t.Fatalf("installer = %v, %v", got, err)
	}
}
