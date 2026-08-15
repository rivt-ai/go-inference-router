package install

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"

	llm "github.com/rivt-ai/go-inference-router"
)

// Verifier authenticates the registry manifest before any artifact named by it
// is considered.
//
// This is an interface rather than a fixed Ed25519 check so the trust model is
// a deployment choice. A long-lived signing key and a keyless Sigstore identity
// answer "who published this?" in genuinely different ways — one binds to a
// key you must protect and rotate, the other to a workflow identity recorded in
// a public transparency log — and which is right depends on how the operator
// distributes binaries, not on anything the installer knows.
//
// The installer's other guarantees are unaffected: whatever authenticates the
// manifest, artifacts are still size- and digest-checked on download and
// re-verified against their recorded digest before every launch.
type Verifier interface {
	// SidecarSuffix is appended to the registry URL to locate the signature.
	// Formats differ (".sig" for a raw signature, ".sigstore.json" for a
	// Sigstore bundle), so the verifier names the file it knows how to read.
	SidecarSuffix() string

	// VerifyManifest reports whether body is authentic according to sidecar.
	// It takes a context because a verifier may need the network — Sigstore
	// can consult a transparency log — even though the key-based one does not.
	VerifyManifest(ctx context.Context, body, sidecar []byte) error
}

// ErrUnverified reports a manifest that failed authentication. Verifiers wrap
// it so a caller can distinguish "not authentic" from "could not check", which
// matters when a verifier depends on a network service that may be down.
//
// This is an alias for the root module's value, not a second sentinel: the
// identity is what errors.Is compares, so a verifier that names either one is
// recognised here. It lives in the root module so an implementation does not
// have to import router — and its dependency tree — merely to say "unverified".
var ErrUnverified = llm.ErrUnverified

// KeyVerifier authenticates with a compiled-in Ed25519 public key. This is the
// original behavior and remains the default.
type KeyVerifier struct {
	key ed25519.PublicKey
}

// NewKeyVerifier returns a Verifier for one trusted Ed25519 public key.
func NewKeyVerifier(key ed25519.PublicKey) *KeyVerifier { return &KeyVerifier{key: key} }

// SidecarSuffix implements Verifier.
func (v *KeyVerifier) SidecarSuffix() string { return ".sig" }

// VerifyManifest implements Verifier.
func (v *KeyVerifier) VerifyManifest(_ context.Context, body, sidecar []byte) error {
	if len(v.key) != ed25519.PublicKeySize {
		return errors.New("registry public key is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sidecar)))
	if err != nil || !ed25519.Verify(v.key, body, signature) {
		return ErrUnverified
	}
	return nil
}
