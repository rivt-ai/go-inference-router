package install

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKeyVerifier(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	body := []byte(`{"version":1,"artifacts":[]}`)
	sidecar := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, body)))
	verifier := NewKeyVerifier(public)

	if verifier.SidecarSuffix() != ".sig" {
		t.Fatalf("sidecar suffix = %q", verifier.SidecarSuffix())
	}
	if err := verifier.VerifyManifest(context.Background(), body, sidecar); err != nil {
		t.Fatalf("a signature from the trusted key must verify: %v", err)
	}
	// A failure has to be distinguishable from "could not check", because a
	// verifier that depends on a network service can fail for either reason.
	if err := verifier.VerifyManifest(context.Background(), []byte(`{"version":1}`), sidecar); !errors.Is(err, ErrUnverified) {
		t.Fatalf("a modified manifest must report ErrUnverified, got %v", err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if err := NewKeyVerifier(other).VerifyManifest(context.Background(), body, sidecar); !errors.Is(err, ErrUnverified) {
		t.Fatalf("an untrusted key must report ErrUnverified, got %v", err)
	}
	if err := NewKeyVerifier(nil).VerifyManifest(context.Background(), body, sidecar); err == nil {
		t.Fatal("an absent key must be an error, not a silent pass")
	}
}

// The installer must fetch the sidecar the verifier names, or two incompatible
// signature formats would have to share one URL. A Sigstore deployment publishes
// providers.json.sigstore.json, not providers.json.sig.
func TestInstallerFetchesTheSidecarTheVerifierNames(t *testing.T) {
	manifest := []byte(`{"version":1,"artifacts":[]}`)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/registry.json") {
			_, _ = w.Write(manifest)
			return
		}
		_, _ = w.Write([]byte("bundle"))
	}))
	defer t.Cleanup(server.Close)

	installer := New(server.URL+"/registry.json", stubVerifier{suffix: ".sigstore.json"},
		t.TempDir(), server.Client(), nil)
	// The registry has no artifacts, so Plan fails on lookup -- after the
	// fetch and verification this test is about.
	_, _ = installer.Plan(context.Background(), "openai", "")

	if len(paths) != 2 || paths[1] != "/registry.json.sigstore.json" {
		t.Fatalf("requested %v, want the manifest then /registry.json.sigstore.json", paths)
	}
}

// A verifier that rejects must fail the manifest rather than being ignored.
func TestInstallerRejectsUnverifiedManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":1,"artifacts":[]}`))
	}))
	defer t.Cleanup(server.Close)

	installer := New(server.URL+"/registry.json", stubVerifier{suffix: ".sig", err: ErrUnverified},
		t.TempDir(), server.Client(), nil)
	if _, err := installer.Plan(context.Background(), "openai", ""); !errors.Is(err, ErrUnverified) {
		t.Fatalf("Plan error = %v, want ErrUnverified", err)
	}
}

type stubVerifier struct {
	suffix string
	err    error
}

func (s stubVerifier) SidecarSuffix() string { return s.suffix }

func (s stubVerifier) VerifyManifest(context.Context, []byte, []byte) error { return s.err }

// TestSigstoreShapeSatisfiesVerifier guards the seam from the side that
// depends on it. router/verify/sigstore cannot assert `var _ Verifier` itself
// without importing this package, which would make it depend on the router
// module instead of the root module, so the shape is restated here. If Verifier
// gains or changes a method, this fails to compile and names the module that
// must be updated with it.
func TestSigstoreShapeSatisfiesVerifier(t *testing.T) {
	var _ Verifier = sigstoreShape{}
}

// sigstoreShape mirrors the exported method set of
// github.com/rivt-ai/go-inference-router/router/verify/sigstore.Verifier.
type sigstoreShape struct{}

func (sigstoreShape) SidecarSuffix() string { return ".sigstore.json" }

func (sigstoreShape) VerifyManifest(context.Context, []byte, []byte) error { return nil }
