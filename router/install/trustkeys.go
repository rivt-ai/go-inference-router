package install

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// A shipped binary can only ever trust the keys that were compiled into it, so
// a single trusted key makes rotation impossible: the day it changes, every
// binary already in the wild rejects every new manifest. That also makes a key
// compromise unrecoverable for existing installs, which is the more serious
// half. Trusting a *set* of keys lets a new key be introduced while the old one
// is still accepted, so releases signed either way verify during an overlap
// window and the old key can then be retired.
//
// Key IDs are derived from the key itself rather than assigned, so an ID can
// never name a different key than the one it was minted from, and a manifest
// cannot point a verifier at a key it does not already trust.

// KeyIDSize is the number of bytes of the key digest kept in a key ID. It only
// has to be wide enough to pick one key out of a handful of trusted ones; it is
// not a security boundary, because verification still requires a signature that
// checks out against the named key.
const KeyIDSize = 4

// TrustedKey is one Ed25519 public key the installer will accept manifests
// from, paired with the short ID that identifies it in a signature.
type TrustedKey struct {
	ID  string
	Key ed25519.PublicKey
}

// KeyID returns the stable short identifier for a public key.
func KeyID(key ed25519.PublicKey) string {
	digest := sha256.Sum256(key)
	return hex.EncodeToString(digest[:KeyIDSize])
}

// NewTrustedKey pairs a public key with its derived ID.
func NewTrustedKey(key ed25519.PublicKey) TrustedKey {
	return TrustedKey{ID: KeyID(key), Key: key}
}

// ParseTrustedKeys decodes a comma-separated list of base64 Ed25519 public
// keys. Whitespace and empty entries are ignored so a list can be split across
// lines in configuration, and duplicates collapse so that overlapping sources
// (a compiled-in key that is also named in configuration) do not produce two
// entries for one key.
func ParseTrustedKeys(value string) ([]TrustedKey, error) {
	var keys []TrustedKey
	seen := map[string]bool{}
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(field)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("registry public key must be base64-encoded Ed25519")
		}
		trusted := NewTrustedKey(decoded)
		if seen[trusted.ID] {
			continue
		}
		seen[trusted.ID] = true
		keys = append(keys, trusted)
	}
	return keys, nil
}

// signatureEnvelope is the signed-manifest sidecar. One manifest carries one
// signature per signing key, which is what makes a rotation overlap possible:
// during the window the release is signed by both the outgoing and incoming
// key, so binaries trusting either one verify the same bytes.
type signatureEnvelope struct {
	Version    int                 `json:"version"`
	Signatures []manifestSignature `json:"signatures"`
}

type manifestSignature struct {
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

// verifyManifest reports whether body carries a valid signature from one of the
// trusted keys.
//
// Two sidecar encodings are accepted. The envelope form names a key ID, so a
// verifier checks only the signature that claims to be from a key it holds. The
// legacy form is a bare base64 signature with no key ID; it is tried against
// every trusted key, which is what the single-key format could always do
// implicitly. Accepting both is what lets binaries built before this change
// keep verifying new manifests, and new binaries keep verifying old ones.
func verifyManifest(keys []TrustedKey, body, sidecar []byte) bool {
	if len(keys) == 0 {
		return false
	}
	envelope, ok := parseEnvelope(sidecar)
	if !ok {
		return verifyLegacy(keys, body, sidecar)
	}
	for _, candidate := range envelope.Signatures {
		signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(candidate.Signature))
		if err != nil {
			continue
		}
		for _, key := range keys {
			if key.ID == candidate.KeyID && ed25519.Verify(key.Key, body, signature) {
				return true
			}
		}
	}
	return false
}

func parseEnvelope(sidecar []byte) (signatureEnvelope, bool) {
	trimmed := strings.TrimSpace(string(sidecar))
	if !strings.HasPrefix(trimmed, "{") {
		return signatureEnvelope{}, false
	}
	var envelope signatureEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return signatureEnvelope{}, false
	}
	return envelope, envelope.Version == 1 && len(envelope.Signatures) > 0
}

func verifyLegacy(keys []TrustedKey, body, sidecar []byte) bool {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sidecar)))
	if err != nil {
		return false
	}
	for _, key := range keys {
		if ed25519.Verify(key.Key, body, signature) {
			return true
		}
	}
	return false
}

// KeyIDs lists the trusted key IDs, for diagnostics when verification fails.
func KeyIDs(keys []TrustedKey) string {
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		ids = append(ids, key.ID)
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}
