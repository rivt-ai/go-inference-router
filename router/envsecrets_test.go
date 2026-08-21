package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivt-ai/go-inference-router/router/config"
)

func TestEnvResolverResolvesEnvAndFile(t *testing.T) {
	t.Setenv("INFROUTER_ENV_SECRET", "token")
	value, err := (EnvResolver{}).Resolve(config.SecretRef{Env: "INFROUTER_ENV_SECRET"})
	if err != nil || value != "token" {
		t.Fatalf("env = %q, %v", value, err)
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("filetoken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err = (EnvResolver{}).Resolve(config.SecretRef{File: path})
	if err != nil || value != "filetoken" {
		t.Fatalf("file = %q, %v", value, err)
	}
}

func TestEnvResolverNamesTheOptInForKeychainRefs(t *testing.T) {
	if _, err := (EnvResolver{}).Resolve(config.SecretRef{Env: "INFROUTER_UNSET_SECRET"}); err == nil {
		t.Fatal("unset env must fail")
	}
	for _, ref := range []config.SecretRef{{Keychain: "k"}, {Secret: "s"}, {}} {
		_, err := (EnvResolver{}).Resolve(ref)
		if err == nil {
			t.Fatalf("ref %+v must fail", ref)
		}
		if (ref.Keychain != "" || ref.Secret != "") && !strings.Contains(err.Error(), "router/secret") {
			t.Fatalf("error should name the opt-in package: %v", err)
		}
	}
}
