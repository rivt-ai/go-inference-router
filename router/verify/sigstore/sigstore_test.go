package sigstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rivt-ai/go-inference-router/router/install"
)

// An unpinned policy is the sharp edge of keyless verification: a bundle that
// verifies cryptographically proves only that somebody signed it and logged it,
// because anyone can obtain a certificate. The constructor must refuse rather
// than default, so a misconfiguration fails at startup instead of silently
// accepting any signer.
func TestNewVerifierRequiresAPinnedIdentity(t *testing.T) {
	root := []byte(`{}`)
	cases := map[string]Policy{
		"no issuer":       {Identity: "https://example.test/wf@refs/heads/main", TrustedRootJSON: root},
		"no identity":     {Issuer: "https://token.actions.githubusercontent.com", TrustedRootJSON: root},
		"no trusted root": {Issuer: "https://token.actions.githubusercontent.com", Identity: "https://example.test/wf@refs/heads/main"},
	}
	for name, policy := range cases {
		if _, err := NewVerifier(policy); err == nil {
			t.Errorf("%s: NewVerifier must fail, got nil error", name)
		}
	}
}

// A malformed trusted root must be reported at construction, not deferred to
// the first verification attempt during an install.
func TestNewVerifierRejectsMalformedTrustedRoot(t *testing.T) {
	_, err := NewVerifier(Policy{
		Issuer:          "https://token.actions.githubusercontent.com",
		Identity:        "https://example.test/wf@refs/heads/main",
		TrustedRootJSON: []byte("not json"),
	})
	if err == nil || !strings.Contains(err.Error(), "trusted root") {
		t.Fatalf("error = %v, want a trusted-root parse failure", err)
	}
}

// A malformed bundle must classify as unverified rather than as some other
// failure, so the installer can tell "not authentic" from "could not check".
func TestVerifyManifestRejectsMalformedBundle(t *testing.T) {
	verifier := &Verifier{}
	err := verifier.VerifyManifest(context.Background(), []byte(`{}`), []byte("not a bundle"))
	if !errors.Is(err, install.ErrUnverified) {
		t.Fatalf("error = %v, want install.ErrUnverified", err)
	}
}

// The identity in docs/integration.md is what integrators copy into their own
// Policy, and it names a workflow file by path. Renaming or deleting that
// workflow leaves the documented identity pointing at a workflow that will
// never sign anything: every host pinning it then fails to install, and no test
// that only checks the *shape* of an identity notices. Resolving the documented
// path against the tree is what turns that into a failing build here.
func TestDocumentedReleaseIdentityNamesTheSigningWorkflow(t *testing.T) {
	// The module root is router/, so the repository root — and the docs and
	// workflows that live only there — is two levels up. A consumer building
	// this module as a dependency has neither, hence the skip rather than a
	// failure.
	root := filepath.Join("..", "..", "..")
	docs, err := os.ReadFile(filepath.Join(root, "docs", "integration.md"))
	if err != nil {
		t.Skipf("integration docs unavailable outside the repository: %v", err)
	}

	// The identity is split across two Go string literals in the example, so
	// match the workflow path and ref rather than the whole SAN.
	match := regexp.MustCompile(`\.github/workflows/([\w.-]+)@(refs/[\w./-]+)`).FindSubmatch(docs)
	if match == nil {
		t.Fatal("docs/integration.md pins no workflow identity; the keyless example must name one")
	}
	workflow, ref := string(match[1]), string(match[2])

	body, err := os.ReadFile(filepath.Join(root, ".github", "workflows", workflow))
	if err != nil {
		t.Fatalf("documented identity names .github/workflows/%s, which does not exist: %v", workflow, err)
	}
	// A workflow that does not sign cannot produce the bundle the identity
	// claims, which is the failure a rename would otherwise hide behind a path
	// that happens to resolve.
	if !bytes.Contains(body, []byte("INFROUTER_SIGSTORE")) {
		t.Errorf("documented identity names %s, which does not sign the manifest", workflow)
	}
	// Fulcio issues refs/heads/<branch> for a branch and refs/tags/<tag> for a
	// tag. Pinning the wrong one accepts runs the release process never makes.
	if ref != "refs/heads/main" {
		t.Errorf("documented identity pins ref %q; releases run from main", ref)
	}
}
