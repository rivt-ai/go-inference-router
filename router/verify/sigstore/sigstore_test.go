package sigstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
// that only checks the *shape* of an identity notices. Resolving the pinned
// path against the tree is what turns that into a failing build here.
func TestReleasePolicyNamesTheSigningWorkflow(t *testing.T) {
	policy := ReleasePolicy()

	prefix := "https://github.com/rivt-ai/go-inference-router/.github/workflows/"
	if !strings.HasPrefix(policy.Identity, prefix) {
		t.Fatalf("identity %q is not a workflow in this repository", policy.Identity)
	}
	workflow, ref, found := strings.Cut(strings.TrimPrefix(policy.Identity, prefix), "@")
	if !found {
		t.Fatalf("identity %q pins no ref; without one a run from any branch satisfies the policy",
			policy.Identity)
	}
	// Fulcio issues refs/heads/<branch> for a branch and refs/tags/<tag> for a
	// tag. Pinning the wrong one accepts runs the release process never makes.
	if ref != "refs/heads/main" {
		t.Errorf("identity pins ref %q; releases run from main", ref)
	}

	// The module root is router/, so the workflows — which live only in the
	// repository — are two levels up. A consumer building this module as a
	// dependency has none of them, hence the skip rather than a failure.
	body, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", workflow))
	if err != nil {
		if os.IsNotExist(err) && !repositoryCheckout(t) {
			t.Skipf("workflows unavailable outside the repository: %v", err)
		}
		t.Fatalf("identity names .github/workflows/%s, which does not exist: %v", workflow, err)
	}
	// A workflow that does not sign cannot produce the bundle the identity
	// claims, which is the failure a rename would otherwise hide behind a path
	// that happens to resolve.
	if !bytes.Contains(body, []byte("INFROUTER_SIGSTORE")) {
		t.Errorf("identity names %s, which does not sign the manifest", workflow)
	}
}

// repositoryCheckout reports whether the tests are running inside this
// repository, where a missing workflow is a real failure rather than a
// consumer's ordinary lack of one.
func repositoryCheckout(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join("..", "..", "..", ".github", "workflows"))
	return err == nil
}

// An embedded root that carries no transparency logs verifies nothing while
// still parsing, so checking that NewVerifier accepts it is not enough. This is
// the shape check; the Sigstore E2E run verifies a real bundle against it.
func TestReleasePolicyEmbedsAUsableTrustedRoot(t *testing.T) {
	policy := ReleasePolicy()
	if _, err := NewVerifier(policy); err != nil {
		t.Fatalf("NewVerifier(ReleasePolicy()): %v", err)
	}
	var parsed struct {
		TLogs                  []json.RawMessage `json:"tlogs"`
		CertificateAuthorities []json.RawMessage `json:"certificateAuthorities"`
	}
	if err := json.Unmarshal(policy.TrustedRootJSON, &parsed); err != nil {
		t.Fatalf("embedded trusted root is not JSON: %v", err)
	}
	if len(parsed.TLogs) == 0 {
		t.Error("embedded trusted root has no transparency logs; " +
			"`cosign trusted-root create` produces this, `cosign initialize` does not")
	}
	if len(parsed.CertificateAuthorities) == 0 {
		t.Error("embedded trusted root has no certificate authorities")
	}
}

// ReleasePolicy hands out the shared embedded bytes; a caller that adjusts its
// copy must not change what the next caller verifies against.
func TestReleasePolicyCopiesTheTrustedRoot(t *testing.T) {
	policy := ReleasePolicy()
	policy.TrustedRootJSON[0] = 'x'
	if bytes.Equal(policy.TrustedRootJSON, ReleasePolicy().TrustedRootJSON) {
		t.Fatal("mutating a returned policy changed the embedded trusted root")
	}
}
