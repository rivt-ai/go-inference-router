package install

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return public, private
}

func envelopeFor(t *testing.T, body []byte, keys ...ed25519.PrivateKey) []byte {
	t.Helper()
	envelope := signatureEnvelope{Version: 1}
	for _, key := range keys {
		public, ok := key.Public().(ed25519.PublicKey)
		if !ok {
			t.Fatal("private key did not yield an ed25519 public key")
		}
		envelope.Signatures = append(envelope.Signatures, manifestSignature{
			KeyID:     KeyID(public),
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, body)),
		})
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return encoded
}

// The whole point of a key set: during a rotation the manifest is signed by
// both keys, and binaries trusting only the old key, only the new key, or both
// all verify the same bytes. Without this a rotation orphans every binary
// already in the wild.
func TestVerifyManifestAcceptsEitherKeyDuringRotation(t *testing.T) {
	body := []byte(`{"version":1,"artifacts":[]}`)
	oldPublic, oldPrivate := newKey(t)
	newPublic, newPrivate := newKey(t)
	sidecar := envelopeFor(t, body, oldPrivate, newPrivate)

	cases := map[string][]TrustedKey{
		"only the outgoing key": {NewTrustedKey(oldPublic)},
		"only the incoming key": {NewTrustedKey(newPublic)},
		"both keys":             {NewTrustedKey(oldPublic), NewTrustedKey(newPublic)},
	}
	for name, keys := range cases {
		if !verifyManifest(keys, body, sidecar) {
			t.Errorf("%s: verification failed, want success", name)
		}
	}
}

// Retiring a key must actually retire it: once a build stops trusting the old
// key, a manifest signed only by that key is rejected.
func TestVerifyManifestRejectsRetiredKey(t *testing.T) {
	body := []byte(`{"version":1,"artifacts":[]}`)
	_, retired := newKey(t)
	current, _ := newKey(t)

	if verifyManifest([]TrustedKey{NewTrustedKey(current)}, body, envelopeFor(t, body, retired)) {
		t.Fatal("a manifest signed only by a retired key must not verify")
	}
}

func TestVerifyManifestRejectsTamperedBodyAndUnknownKeys(t *testing.T) {
	body := []byte(`{"version":1,"artifacts":[]}`)
	public, private := newKey(t)
	sidecar := envelopeFor(t, body, private)
	trusted := []TrustedKey{NewTrustedKey(public)}

	if verifyManifest(trusted, []byte(`{"version":1,"artifacts":[{}]}`), sidecar) {
		t.Error("a modified manifest must not verify")
	}
	if verifyManifest(nil, body, sidecar) {
		t.Error("an empty key set must never verify")
	}
	// A signature that claims an ID we do not hold is not checked at all, and a
	// signature whose ID we hold but whose bytes are wrong must still fail.
	forged := strings.Replace(string(sidecar), KeyID(public), "deadbeef", 1)
	if verifyManifest(trusted, body, []byte(forged)) {
		t.Error("a signature naming an untrusted key must not verify")
	}
}

// A key ID cannot be claimed by a key that does not hash to it, so an attacker
// cannot get their signature checked against our key by relabelling it.
func TestVerifyManifestRejectsMislabelledSignature(t *testing.T) {
	body := []byte(`{"version":1,"artifacts":[]}`)
	trustedPublic, _ := newKey(t)
	_, attacker := newKey(t)

	sidecar := envelopeFor(t, body, attacker)
	mislabelled := strings.Replace(
		string(sidecar), envelopeKeyID(t, sidecar), KeyID(trustedPublic), 1)

	if verifyManifest([]TrustedKey{NewTrustedKey(trustedPublic)}, body, []byte(mislabelled)) {
		t.Fatal("a signature relabelled with a trusted key id must not verify")
	}
}

func envelopeKeyID(t *testing.T, sidecar []byte) string {
	t.Helper()
	envelope, ok := parseEnvelope(sidecar)
	if !ok || len(envelope.Signatures) == 0 {
		t.Fatal("could not read the envelope back")
	}
	return envelope.Signatures[0].KeyID
}

// Binaries built before this change emit and expect a bare base64 signature.
// Both directions have to keep working or a rotation is not the only thing that
// breaks -- the format change itself would orphan installs.
func TestVerifyManifestAcceptsLegacyBareSignature(t *testing.T) {
	body := []byte(`{"version":1,"artifacts":[]}`)
	public, private := newKey(t)
	legacy := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, body)))

	if !verifyManifest([]TrustedKey{NewTrustedKey(public)}, body, legacy) {
		t.Fatal("a legacy bare signature from a trusted key must verify")
	}
	other, _ := newKey(t)
	if verifyManifest([]TrustedKey{NewTrustedKey(other)}, body, legacy) {
		t.Fatal("a legacy signature from an untrusted key must not verify")
	}
}

func TestParseTrustedKeys(t *testing.T) {
	first, _ := newKey(t)
	second, _ := newKey(t)
	encode := base64.StdEncoding.EncodeToString

	keys, err := ParseTrustedKeys(" " + encode(first) + " , " + encode(second) + " ,, ")
	if err != nil {
		t.Fatalf("ParseTrustedKeys: %v", err)
	}
	if len(keys) != 2 || keys[0].ID != KeyID(first) || keys[1].ID != KeyID(second) {
		t.Fatalf("keys = %#v", keys)
	}
	// Overlapping sources must not produce two entries for one key.
	duplicated, err := ParseTrustedKeys(encode(first) + "," + encode(first))
	if err != nil || len(duplicated) != 1 {
		t.Fatalf("duplicates did not collapse: %#v (%v)", duplicated, err)
	}
	if empty, err := ParseTrustedKeys(""); err != nil || len(empty) != 0 {
		t.Fatalf("empty list = %#v (%v)", empty, err)
	}
	if _, err := ParseTrustedKeys("not-base64!"); err == nil {
		t.Error("malformed key must be rejected")
	}
	if _, err := ParseTrustedKeys(encode([]byte("too short"))); err == nil {
		t.Error("wrong-length key must be rejected")
	}
}
