package secret_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/secret"
)

type keychain map[string]string

func (k keychain) Get(_, account string) (string, error) {
	value, ok := k[account]
	if !ok {
		return "", secret.ErrNotFound
	}
	return value, nil
}

func (k keychain) Set(_, account, value string) error { k[account] = value; return nil }
func (k keychain) Delete(_, account string) error     { delete(k, account); return nil }

type failingKeychain struct {
	getErr error
	setErr error
}

func (k failingKeychain) Get(string, string) (string, error) { return "", k.getErr }
func (k failingKeychain) Set(string, string, string) error   { return k.setErr }
func (failingKeychain) Delete(string, string) error          { return nil }

func TestResolverSupportsReferencesWithoutLeakingPlaintext(t *testing.T) {
	t.Setenv("TEST_LLM_KEY", "from-env")
	dir := t.TempDir()
	file := filepath.Join(dir, "key")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := secret.NewEncryptedStore(filepath.Join(dir, "secrets.age"), identity)
	if err := store.Set("stored", "from-store"); err != nil {
		t.Fatal(err)
	}
	chain := keychain{"native": "from-keychain"}
	resolver := secret.Resolver{Keychain: chain, Store: store}

	checks := []struct {
		ref  config.SecretRef
		want string
	}{
		{config.SecretRef{Env: "TEST_LLM_KEY"}, "from-env"},
		{config.SecretRef{File: file}, "from-file"},
		{config.SecretRef{Keychain: "native"}, "from-keychain"},
		{config.SecretRef{Secret: "stored"}, "from-store"},
	}
	for _, check := range checks {
		got, err := resolver.Resolve(check.ref)
		if err != nil || got != check.want {
			t.Fatalf("Resolve(%#v) = %q, %v", check.ref, got, err)
		}
	}
	encrypted, err := os.ReadFile(filepath.Join(dir, "secrets.age"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), "from-store") {
		t.Fatal("encrypted store contains plaintext")
	}
}

func TestResolverRejectsPermissiveSecretFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are unavailable")
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (secret.Resolver{}).Resolve(config.SecretRef{File: path}); err == nil {
		t.Fatal("expected owner-only permission error")
	}
}

func TestIdentityFallsBackToStableOwnerOnlyFile(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "secrets.key")
	storePath := filepath.Join(dir, "secrets.age")
	chain := failingKeychain{getErr: secret.ErrUnavailable}
	first, err := secret.LoadOrCreateIdentity(chain, identityPath, storePath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secret.LoadOrCreateIdentity(chain, identityPath, storePath)
	if err != nil || second.String() != first.String() {
		t.Fatalf("second identity = %v, %v", second, err)
	}
	info, err := os.Stat(identityPath)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %v, %v", info.Mode().Perm(), err)
	}
}

func TestIdentityDoesNotReplaceMissingKeyForExistingStore(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "secrets.key")
	storePath := filepath.Join(dir, "secrets.age")
	if err := os.WriteFile(storePath, []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := secret.LoadOrCreateIdentity(failingKeychain{getErr: secret.ErrUnavailable}, identityPath, storePath)
	if err == nil || !strings.Contains(err.Error(), "existing encrypted store") {
		t.Fatalf("LoadOrCreateIdentity error = %v", err)
	}
	if _, err := os.Stat(identityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback identity was created: %v", err)
	}
}

func TestIdentityDoesNotHideKeychainFailure(t *testing.T) {
	want := errors.New("keychain is locked")
	_, err := secret.LoadOrCreateIdentity(failingKeychain{getErr: want}, filepath.Join(t.TempDir(), "key"), "")
	if !errors.Is(err, want) {
		t.Fatalf("LoadOrCreateIdentity error = %v", err)
	}
}
