package install_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/router/install"
)

func TestSignedPlanRequiresApprovalAndInstallsAtomically(t *testing.T) {
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
	defer server.Close()
	manifest, err = json.Marshal(install.Manifest{Version: 1, Artifacts: []install.Artifact{{
		Provider: "openai", Version: "v1.2.3", Protocol: llmv1.Protocol,
		OS: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL + "/provider",
		Size: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	installer := install.New(server.URL+"/registry.json", []install.TrustedKey{install.NewTrustedKey(public)}, t.TempDir(), server.Client(), nil)
	plan, err := installer.Plan(context.Background(), "openai", "")
	if err != nil || plan.Provider != "openai" || plan.Version != "v1.2.3" {
		t.Fatalf("Plan = %#v, %v", plan, err)
	}
	path, err := installer.Approve(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(artifact) {
		t.Fatalf("installed = %q, %v", got, err)
	}
	if located, ok, err := installer.Locate("openai", "v1.2.3"); err != nil || !ok || located != path {
		t.Fatalf("Locate = %q, %v, %v", located, ok, err)
	}
	if _, err := installer.Approve(context.Background(), plan.ID); err == nil {
		t.Fatal("approval must be one-use")
	}
}

func TestLocateRejectsTamperedInstalledBinary(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	artifact := []byte("trusted")
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
		}
	}))
	defer server.Close()
	manifest, _ = json.Marshal(install.Manifest{Version: 1, Artifacts: []install.Artifact{{
		Provider: "openai", Version: "v1", Protocol: llmv1.Protocol,
		OS: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL + "/provider",
		Size: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
	}}})
	installer := install.New(server.URL+"/registry.json", []install.TrustedKey{install.NewTrustedKey(public)}, t.TempDir(), server.Client(), nil)
	plan, err := installer.Plan(context.Background(), "openai", "v1")
	if err != nil {
		t.Fatal(err)
	}
	path, err := installer.Approve(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("swapped"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := installer.Locate("openai", "v1"); err == nil {
		t.Fatal("tampered provider binary was accepted")
	}
}

func TestManifestSignatureIsRequired(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":1,"artifacts":[]}`))
	}))
	defer server.Close()
	installer := install.New(server.URL, []install.TrustedKey{install.NewTrustedKey(public)}, t.TempDir(), server.Client(), nil)
	if _, err := installer.Plan(context.Background(), "openai", ""); err == nil {
		t.Fatal("unsigned manifest was accepted")
	}
}

func TestStableSelectionAvailabilityAndRemoval(t *testing.T) {
	installer, cache := testInstaller(t, "v1.9.9-rc.1", "1.9.9", "v1.9.9", "v2.0.0-rc.1", "legacy")
	ctx := context.Background()
	for _, version := range []string{"1.9.9", "v2.0.0-rc.1", "legacy"} {
		plan, err := installer.Plan(ctx, "openai", version)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := installer.Approve(ctx, plan.ID); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := installer.Plan(ctx, "openai", "")
	if err != nil || plan.Version != "v1.9.9" {
		t.Fatalf("stable Plan = %#v, %v", plan, err)
	}
	if path, ok, err := installer.Locate("openai", ""); err != nil || !ok || !containsVersion(path, "1.9.9") {
		t.Fatalf("stable Locate = %q, %v, %v", path, ok, err)
	}
	available, err := installer.Available(ctx, "openai")
	if err != nil || available.InstalledVersion != "1.9.9" || available.AvailableVersion != "v1.9.9" || available.UpdateAvailable {
		t.Fatalf("Available = %#v, %v", available, err)
	}
	wantVersions := []string{"v2.0.0-rc.1", "1.9.9", "legacy"}
	if len(available.InstalledVersions) != len(wantVersions) {
		t.Fatalf("installed versions = %#v", available.InstalledVersions)
	}
	for index, want := range wantVersions {
		if available.InstalledVersions[index] != want {
			t.Fatalf("installed versions = %#v", available.InstalledVersions)
		}
	}
	removed, err := installer.Remove(ctx, "openai", "v2.0.0-rc.1")
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(cache, "openai", "v2.0.0-rc.1")); !os.IsNotExist(err) {
		t.Fatalf("removed version still exists: %v", err)
	}
	if removed, err := installer.Remove(ctx, "openai", "v2.0.0-rc.1"); err != nil || removed {
		t.Fatalf("idempotent Remove = %v, %v", removed, err)
	}
}

func TestAvailableInventoriesProvidersTheRegistryNoLongerOffers(t *testing.T) {
	installer, cache := testInstaller(t, "v1.0.0")
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(cache, "withdrawn", "v0.9.0"), 0o700); err != nil {
		t.Fatal(err)
	}
	available, err := installer.Available(ctx, "withdrawn")
	if err != nil {
		t.Fatalf("Available = %#v, %v", available, err)
	}
	if available.AvailableVersion != "" || available.UpdateAvailable {
		t.Fatalf("Available = %#v", available)
	}
	if available.InstalledVersion != "v0.9.0" ||
		len(available.InstalledVersions) != 1 || available.InstalledVersions[0] != "v0.9.0" {
		t.Fatalf("Available = %#v", available)
	}
	if removed, err := installer.Remove(ctx, "withdrawn", "v0.9.0"); err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
}

func TestAvailableEncodesAnEmptyInventoryAsAnArray(t *testing.T) {
	installer, cache := testInstaller(t, "v1.0.0")
	// A provider directory holding no version directories must not report a
	// null inventory when the same query on a missing directory reports [].
	if err := os.MkdirAll(filepath.Join(cache, "openai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "openai", "notes.txt"), []byte("stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	available, err := installer.Available(context.Background(), "openai")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(available)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"installed_versions":[]`) {
		t.Fatalf("Available JSON = %s", encoded)
	}
}

func testInstaller(t *testing.T, versions ...string) (*install.Installer, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("provider executable")
	digest := sha256.Sum256(binary)
	var manifest []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/provider":
			_, _ = w.Write(binary)
		case "/registry.json":
			_, _ = w.Write(manifest)
		case "/registry.json.sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifest))))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	artifacts := make([]install.Artifact, 0, len(versions))
	for _, version := range versions {
		artifacts = append(artifacts, install.Artifact{
			Provider: "openai", Version: version, Protocol: llmv1.Protocol,
			OS: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL + "/provider",
			Size: int64(len(binary)), SHA256: hex.EncodeToString(digest[:]),
		})
	}
	manifest, err = json.Marshal(install.Manifest{Version: 1, Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	return install.New(server.URL+"/registry.json", []install.TrustedKey{install.NewTrustedKey(public)}, cache, server.Client(), nil), cache
}

func containsVersion(path, version string) bool {
	for path != "." && path != string(filepath.Separator) {
		if filepath.Base(path) == version {
			return true
		}
		path = filepath.Dir(path)
	}
	return false
}
