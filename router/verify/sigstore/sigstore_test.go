package sigstore

import (
	"context"
	"errors"
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
