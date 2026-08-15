# ADR 0007 — Keyless registry verification as an opt-in verifier

**Status:** proposed (draft — not accepted)
**Supersedes:** nothing. Extends ADR 0006 (Provider Process threat model).

## Context

The provider registry manifest is authenticated with one Ed25519 public key
compiled into the binary by `-ldflags`. That has two consequences that are
properties of the *design*, not of how carefully the private key is stored:

1. **Rotation orphans installs.** A binary can only trust keys compiled into it,
   so the day the key changes, every binary in the wild rejects every new
   manifest.
2. **Compromise is unrecoverable** for anyone who already installed. The remedy
   is a new binary, delivered by some channel that is not the registry.

Separately, §4.5 of the integration spec notes that nobody currently holds the
signing key and nothing hosts the manifest — so the custody question is open
rather than settled badly.

Sigstore answers a different question than a signing key does. A key answers
"was this signed by the key we shipped?". Keyless signing answers "was this
produced by the release workflow of this repository?" — binding an ephemeral
key to an OIDC identity via a short-lived Fulcio certificate, with the signature
recorded in a public transparency log. There is no long-lived secret, so there
is nothing to rotate and nothing to steal.

## Decision

Make manifest verification a seam (`install.Verifier`) rather than a fixed
Ed25519 check, and ship the Sigstore implementation as a **separate Go module**
that nothing in `router/` imports.

- `install.Verifier` has two methods: `SidecarSuffix()` and `VerifyManifest()`.
  The suffix belongs to the verifier because the formats are incompatible and
  must not share a URL (`.sig` vs `.sigstore.json`).
- `install.KeyVerifier` preserves today's behavior and remains the default.
- `router/verify/sigstore` implements the seam using `sigstore-go`. It is its
  own module for the same reason each SDK adapter is (ADR 0003): importing it
  costs `sigstore-go` and its transitive dependencies, and a host keeping the
  key-based root should not pay for them.
- A host opts in through `router.Options.RegistryVerifier`, mirroring how
  `Options.Secrets` already gates age/keyring/dbus.

The release pipeline publishes **both** sidecars. Publishing only the bundle
would strand every binary already installed — the exact failure this is meant
to avoid.

## Consequences

**Gained.** No long-lived signing key to hold, rotate, or recover from. The
signer identity is publicly auditable rather than asserted. The custody question
in spec §4.5 stops being blocking, because there is nothing to have custody of.

**Cost.** The trust root moves from a key we ship to Sigstore's trusted root.
This ADR takes that root as *bytes* (`Policy.TrustedRootJSON`) rather than
fetching it, so verification stays offline and deterministic and cannot be
steered by whoever answers a TUF request — at the price of refreshing it when
Sigstore rotates its own roots. That is release engineering, not runtime.

**Cost.** Verification has more moving parts than one `ed25519.Verify`, and the
dependency footprint is large. Confining it to its own module bounds the blast
radius to hosts that ask for it.

**The module stays a leaf, and that is what makes it publishable.** The seam's
methods take only stdlib types, so Go satisfies `install.Verifier` implicitly
and an implementation never needs to import it. The one thing both sides must
agree on is the identity of `ErrUnverified`, which distinguishes "not authentic"
from "could not check". That sentinel therefore lives in the **root** module —
which has no dependencies — and `install.ErrUnverified` aliases it, so the
identity `errors.Is` compares is unchanged.

Had it stayed in `router/install`, this module would have required *router*
rather than the root: the repository's first depth-2 module, and one that
`check-module-versions` and `release-modules.yml` cannot publish, since both
assume every published module pins the root at the release version and tags
them all at a single commit. A module whose `go.sum` can only be correct after
a *sibling's* tag exists does not fit a one-pass tag job. Naming the sentinel
from the root instead keeps this module structurally identical to the SDK
adapters, so it publishes through the existing flow with no new release stage.
The cost is that the compile-time `var _ install.Verifier` assertion cannot live
here; the contract is asserted from `router/install`'s tests instead.

**Policy must be pinned, and this is the sharp edge.** Verifying a bundle
without pinning an identity proves only that *somebody* signed it and logged it
— anyone can obtain a certificate. `Policy` therefore requires both an issuer
and an identity, with no defaults. Pinning the workflow **and its ref** matters:
without the ref, a run from any branch satisfies the policy, which is a
publishing path available to anyone with push access to a branch.

## Alternatives considered

**Trust a set of keys instead (multi-key rotation).** Cheaper, keeps the current
model, and makes rotation survivable rather than removing the problem. Sketched
separately; the two compose — a key set and a Sigstore verifier can coexist
behind this same seam, and adopting one does not waste the other.

**Do nothing until a registry exists.** Rejected on timing: the trust root is
baked into shipped binaries, so it is cheapest to change while there are no
users to strand.

## Open

- Can the installer depend on Sigstore verification at *verify* time in every
  deployment we care about? Offline verification against a pinned trusted root
  avoids a network call, but the trusted root still has to be refreshed.
- Should keyless become the default once a registry is hosted, with the key path
  kept only for private registries?
