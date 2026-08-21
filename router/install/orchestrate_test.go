package install_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/router/install"
)

func signedRegistry(t *testing.T) (*httptest.Server, ed25519.PublicKey, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("provider executable")
	digest := sha256.Sum256(artifact)
	var manifest []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/provider":
			_, _ = w.Write(artifact)
		case "/registry.json":
			_, _ = w.Write(manifest)
		case "/registry.json.sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifest))))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	manifest, err = json.Marshal(install.Manifest{Version: 1, Artifacts: []install.Artifact{{
		Provider: "openai", Version: "v1.2.3", Protocol: llmv1.Protocol,
		OS: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL + "/provider",
		Size: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return server, public, artifact
}

func TestInstallRunsPlanConfirmApprove(t *testing.T) {
	server, public, artifact := signedRegistry(t)
	installer := install.New(server.URL+"/registry.json", install.NewKeyVerifier(public), t.TempDir(), server.Client(), nil)
	var shown install.Plan
	path, err := installer.Install(context.Background(), "openai", "", func(plan install.Plan) (bool, error) {
		shown = plan
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if shown.Provider != "openai" || shown.Version != "v1.2.3" || shown.Size != int64(len(artifact)) || shown.SHA256 == "" {
		t.Fatalf("confirm saw %#v", shown)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(artifact) {
		t.Fatalf("installed = %q, %v", got, err)
	}
}

func TestInstallDeclineInstallsNothing(t *testing.T) {
	server, public, _ := signedRegistry(t)
	cache := t.TempDir()
	installer := install.New(server.URL+"/registry.json", install.NewKeyVerifier(public), cache, server.Client(), nil)
	_, err := installer.Install(context.Background(), "openai", "", func(install.Plan) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, install.ErrDeclined) {
		t.Fatalf("err = %v, want ErrDeclined", err)
	}
	if _, ok, _ := installer.Locate("openai", "v1.2.3"); ok {
		t.Fatal("a declined install must write nothing")
	}
}
