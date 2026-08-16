//go:build sigstoree2e

// Package sigstore's end-to-end test. Separated behind the sigstoree2e build
// tag because it needs artifacts that only exist where cosign can obtain an
// OIDC token: a real bundle over a real manifest, and a real trusted root.
//
// Everything in sigstore_test.go tests rejection — malformed bundles, missing
// policy fields, error classification. None of it exercises the accept path,
// so without this file a verifier that rejects *everything* would pass the
// suite and fail only on a user's machine at install time. That is the failure
// this file exists to catch, so it asserts acceptance first and rejection
// second.
package sigstore_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rivt-ai/go-inference-router/router/install"
	"github.com/rivt-ai/go-inference-router/router/verify/sigstore"
)

// fixture reads a path from the environment and fails loudly when it is
// missing. A skip here would defeat the point: an e2e test that quietly does
// not run is indistinguishable from one that passes.
func fixture(t *testing.T, env string) []byte {
	t.Helper()
	path := os.Getenv(env)
	if path == "" {
		t.Fatalf("%s is unset; the sigstoree2e build tag requires real fixtures", env)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (%s): %v", env, path, err)
	}
	if len(data) == 0 {
		t.Fatalf("%s (%s) is empty", env, path)
	}
	return data
}

func policy(t *testing.T) sigstore.Policy {
	t.Helper()
	issuer := os.Getenv("SIGSTORE_E2E_ISSUER")
	identity := os.Getenv("SIGSTORE_E2E_IDENTITY")
	if issuer == "" || identity == "" {
		t.Fatal("SIGSTORE_E2E_ISSUER and SIGSTORE_E2E_IDENTITY must both be set")
	}
	return sigstore.Policy{
		Issuer:          issuer,
		Identity:        identity,
		TrustedRootJSON: fixture(t, "SIGSTORE_E2E_TRUSTED_ROOT"),
	}
}

// TestVerifiesRealBundle is the assertion the unit tests cannot make: a bundle
// produced by cosign against a real Fulcio certificate and logged in Rekor
// verifies. A wrong issuer string, a stale trusted root, a behavioural change
// in sigstore-go, or a policy that is subtly too strict all fail here rather
// than at install time.
func TestVerifiesRealBundle(t *testing.T) {
	verifier, err := sigstore.NewVerifier(policy(t))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	body := fixture(t, "SIGSTORE_E2E_MANIFEST")
	bundle := fixture(t, "SIGSTORE_E2E_BUNDLE")

	if err := verifier.VerifyManifest(context.Background(), body, bundle); err != nil {
		t.Fatalf("VerifyManifest rejected a genuine bundle: %v", err)
	}
}

// TestRejectsTamperedManifest proves the accept path above is discriminating
// rather than permissive. Verifying the real bundle against a body it does not
// cover must fail, and must fail as ErrUnverified so the installer reports "not
// authentic" rather than "could not check".
func TestRejectsTamperedManifest(t *testing.T) {
	verifier, err := sigstore.NewVerifier(policy(t))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	body := fixture(t, "SIGSTORE_E2E_MANIFEST")
	bundle := fixture(t, "SIGSTORE_E2E_BUNDLE")

	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-1] ^= 0xff

	err = verifier.VerifyManifest(context.Background(), tampered, bundle)
	if err == nil {
		t.Fatal("VerifyManifest accepted a manifest the bundle does not cover")
	}
	if !errors.Is(err, install.ErrUnverified) {
		t.Fatalf("error = %v, want it to wrap install.ErrUnverified", err)
	}
}

// TestRejectsUnpinnedIdentity covers the sharp edge ADR 0007 names: a bundle
// that verifies cryptographically but was signed by someone else must not pass.
// Anyone can obtain a Fulcio certificate, so identity pinning is the only thing
// separating "signed and logged" from "signed by us".
func TestRejectsUnpinnedIdentity(t *testing.T) {
	p := policy(t)
	p.Identity = "https://github.com/rivt-ai/not-this-repo/.github/workflows/release.yml@refs/heads/main"

	verifier, err := sigstore.NewVerifier(p)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	err = verifier.VerifyManifest(context.Background(),
		fixture(t, "SIGSTORE_E2E_MANIFEST"), fixture(t, "SIGSTORE_E2E_BUNDLE"))
	if err == nil {
		t.Fatal("VerifyManifest accepted a bundle signed by an unpinned identity")
	}
	if !errors.Is(err, install.ErrUnverified) {
		t.Fatalf("error = %v, want it to wrap install.ErrUnverified", err)
	}
}

// TestIdentityMatchesReleaseWorkflowShape guards the string that is easiest to
// get wrong and impossible to unit test: the certificate SAN a GitHub Actions
// run actually produces. The identity under test is this workflow's own, so the
// shape — repository URL, .github/workflows/<file>, @<ref> — is what release.yml
// must also match. A rename of the workflow file or a ref that is a tag rather
// than a branch shows up here.
func TestIdentityMatchesReleaseWorkflowShape(t *testing.T) {
	identity := os.Getenv("SIGSTORE_E2E_IDENTITY")
	repository := os.Getenv("GITHUB_SERVER_URL") + "/" + os.Getenv("GITHUB_REPOSITORY")

	if !strings.HasPrefix(identity, repository+"/.github/workflows/") {
		t.Fatalf("identity %q is not a workflow in %q; the release policy pins this shape",
			identity, repository)
	}
	if !strings.Contains(identity, "@refs/") {
		t.Fatalf("identity %q pins no ref; without one a run from any branch satisfies the policy",
			identity)
	}
}
