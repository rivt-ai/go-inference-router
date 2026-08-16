// Package sigstore verifies the provider registry manifest against a Sigstore
// bundle instead of a long-lived signing key.
//
// It lives in the router module rather than a module of its own. Provider
// installation is what the router module is for, and a host that installs
// provider binaries is exactly the host that needs to verify them — so the
// dependency lands on the feature that requires it. An application that only
// wants the chat-completions driver imports provider/openaicompat from the
// dependency-free root module and pays none of this.
//
// # What this changes about trust
//
// A key-based root answers "was this signed by the key we shipped?". That makes
// the key something to protect, rotate, and recover from — and a binary can
// only trust keys compiled into it, so rotation is a distribution problem.
//
// Keyless verification answers a different question: "was this produced by the
// release workflow of this repository?" The signing key is ephemeral, bound to
// an OIDC identity by a short-lived Fulcio certificate and recorded in a public
// transparency log. There is no long-lived secret to hold, so there is nothing
// to rotate and nothing to steal.
//
// The cost is that the trust root moves from a key you ship to Sigstore's
// trusted root, and verification has more moving parts. This package takes the
// trusted root as bytes rather than fetching it, so verification stays offline
// and deterministic; see NewVerifier.
package sigstore

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/rivt-ai/go-inference-router/router/install"
)

// ReleaseIssuer and ReleaseIdentity pin who may sign this repository's registry
// manifest: the release workflow of this repository, running from main.
//
// Fulcio issues refs/heads/<branch> for a branch and refs/tags/<tag> for a tag,
// so this string tracks how release.yml is actually dispatched. Renaming that
// workflow or releasing from somewhere else invalidates it, which is what
// TestReleasePolicyNamesTheSigningWorkflow exists to catch.
const (
	ReleaseIssuer   = "https://token.actions.githubusercontent.com"
	ReleaseIdentity = "https://github.com/rivt-ai/go-inference-router" +
		"/.github/workflows/release.yml@refs/heads/main"
)

// releaseTrustedRoot is the public-good Sigstore trusted root, embedded so
// verification needs no network and no per-host setup.
//
// Refresh it with `make sync-trusted-root` when Sigstore rotates its roots. A
// stale root stops verifying with no code change to announce it, which is why
// the weekly Sigstore E2E run verifies a real bundle against these bytes.
//
//go:embed trusted_root.json
var releaseTrustedRoot []byte

// ReleasePolicy is the policy for the registry this repository publishes.
//
// It exists so hosts do not each re-derive the same three values, since two of
// them are easy to get wrong in ways that fail closed but obscurely: an
// identity missing its ref accepts runs from any branch, and a trusted root
// built rather than fetched contains no transparency logs at all.
//
// A function returning a fresh copy, rather than a package variable, so no
// caller can mutate the root or the identity every other caller relies on.
// Adjust the copy if you need to; a host running its own registry supplies its
// own Policy instead.
func ReleasePolicy() Policy {
	return Policy{
		Issuer:          ReleaseIssuer,
		Identity:        ReleaseIdentity,
		TrustedRootJSON: bytes.Clone(releaseTrustedRoot),
	}
}

// Policy names the workflow identity a manifest must have been signed by.
//
// Both fields are required and neither has a safe default. Verifying a bundle
// without pinning an identity proves only that *somebody* signed it and put it
// in the log, which is not a trust decision — anyone can obtain a certificate.
type Policy struct {
	// Issuer is the OIDC issuer, e.g.
	// "https://token.actions.githubusercontent.com" for GitHub Actions.
	Issuer string

	// Identity is the exact certificate SAN identifying the signer. For a
	// GitHub Actions workflow that is the workflow file at a ref, e.g.
	// "https://github.com/rivt-ai/go-inference-router/.github/workflows/release.yml@refs/heads/main"
	//
	// Pinning the ref matters: without it a workflow run from any branch or
	// fork tag satisfies the policy, which is a publishing path an attacker
	// with push access to a branch could take.
	Identity string

	// IdentityRegexp, when set, is used instead of Identity to match the SAN.
	// Useful when releases are tagged rather than run from a branch. Prefer
	// Identity; a loose pattern is the easiest way to widen this accidentally.
	IdentityRegexp string

	// TrustedRootJSON is the Sigstore trusted root, fetched from TUF ahead of
	// time. ReleasePolicy supplies the public-good root for this repository's
	// registry; a host verifying its own registry obtains one with
	// `cosign initialize` and reads the cached trusted_root.json target.
	//
	// Not `cosign trusted-root create`: despite the name that subcommand does
	// not fetch anything. It builds a root from material passed in its
	// --fulcio/--rekor/--ctfe flags, and with no flags emits a root with no
	// transparency logs and no certificate authorities, which verifies nothing.
	//
	// Taken as bytes rather than fetched at verify time so verification is
	// offline, reproducible, and cannot be steered by whoever answers a TUF
	// request. The cost is that it must be refreshed when Sigstore rotates its
	// own roots, which is a release-engineering task rather than a runtime one.
	TrustedRootJSON []byte
}

// Verifier authenticates a registry manifest against a Sigstore bundle.
type Verifier struct {
	verifier *verify.Verifier
	identity verify.CertificateIdentity
}

// NewVerifier builds a Verifier for one signing identity.
func NewVerifier(policy Policy) (*Verifier, error) {
	if policy.Issuer == "" {
		return nil, errors.New("sigstore policy requires an issuer")
	}
	if policy.Identity == "" && policy.IdentityRegexp == "" {
		return nil, errors.New("sigstore policy requires an identity or identity regexp")
	}
	if len(policy.TrustedRootJSON) == 0 {
		return nil, errors.New("sigstore policy requires a trusted root")
	}
	trustedRoot, err := root.NewTrustedRootFromJSON(policy.TrustedRootJSON)
	if err != nil {
		return nil, fmt.Errorf("parse sigstore trusted root: %w", err)
	}
	identity, err := verify.NewShortCertificateIdentity(
		policy.Issuer, "", policy.Identity, policy.IdentityRegexp)
	if err != nil {
		return nil, fmt.Errorf("build sigstore identity: %w", err)
	}
	// Requiring a transparency-log entry and an observer timestamp is what
	// makes a short-lived certificate meaningful after it expires: the log
	// entry proves the signature existed while the certificate was valid.
	// Without them an expired certificate would simply fail, and a signature
	// could not be verified after roughly ten minutes.
	inner, err := verify.NewVerifier(trustedRoot,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return nil, fmt.Errorf("build sigstore verifier: %w", err)
	}
	return &Verifier{verifier: inner, identity: identity}, nil
}

// SidecarSuffix implements install.Verifier. Cosign writes bundles with this
// suffix by convention, and keeping the key-based ".sig" name would make two
// incompatible formats share one URL.
func (v *Verifier) SidecarSuffix() string { return ".sigstore.json" }

// VerifyManifest implements install.Verifier.
func (v *Verifier) VerifyManifest(_ context.Context, body, sidecar []byte) error {
	var signed bundle.Bundle
	if err := signed.UnmarshalJSON(sidecar); err != nil {
		return fmt.Errorf("%w: malformed sigstore bundle: %w", install.ErrUnverified, err)
	}
	_, err := v.verifier.Verify(&signed, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(body)),
		verify.WithCertificateIdentity(v.identity),
	))
	if err != nil {
		return fmt.Errorf("%w: %w", install.ErrUnverified, err)
	}
	return nil
}

// Verifier must satisfy the installer's seam.
var _ install.Verifier = (*Verifier)(nil)
