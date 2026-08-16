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
Ed25519 check, and implement Sigstore verification **inside the router module**.

- `install.Verifier` has two methods: `SidecarSuffix()` and `VerifyManifest()`.
  The suffix belongs to the verifier because the formats are incompatible and
  must not share a URL (`.sig` vs `.sigstore.json`).
- `router/verify/sigstore` implements the seam using `sigstore-go`, in the
  router module — see the consequence below on why not a module of its own.
- `install.KeyVerifier` preserves today's behavior and remains the default
  until a release ships an embedded trusted root and a pinned identity.
- The seam stays because **private registries need it**: the Sigstore policy
  pins *this* repository's workflow identity, so a host running its own
  registry must supply its own verifier through
  `router.Options.RegistryVerifier`. That is what the seam is for now — not a
  keyless-versus-key choice for hosts using the official registry.

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
dependency footprint is large.

**It lives in the router module, not a module of its own.** An earlier draft
split it out, on the ADR 0003 argument that a host keeping the Ed25519 root
should not pay for `sigstore-go`. That argument does not survive contact with
how the router is actually consumed. Applications embed the router *to load and
install provider binaries*; an application that only wants the
chat-completions driver imports `provider/openaicompat` from the
dependency-free root module and never sees `router` at all. So the hosts paying
for these dependencies are exactly the hosts executing downloaded binaries —
the dependency lands on the feature that needs it.

Keeping it separate also made verification opt-in, which is the wrong default
for a security control: a host that forgets to opt in falls back to the key
path silently. And it made the package the repository's first depth-2 module,
which `check-module-versions` and `release-modules.yml` cannot publish, since
both assume every published module pins the root at the release version and
tags them all at one commit.

The cost is real and bounded: `router` goes from 10 direct dependencies to
around 80. `provider/openaicompat` stays in the root module precisely so that
cost is avoidable.

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

**The accept path is tested against a real bundle, not a fixture.** Unit tests
here can only cover rejection: producing a genuine bundle needs cosign and an
OIDC token. A verifier that refused everything would therefore pass the suite
and fail on a user's machine at install time. `.github/workflows/sigstore-e2e.yml`
signs a manifest with the same `cosign sign-blob` invocation the release script
uses, generates a trusted root, and verifies it with the real verifier. It runs
on pushes to main and weekly — the schedule is the part that matters, because a
trusted root goes stale by the calendar rather than by a code change, and
nothing else would notice. It cannot run on pull requests from forks, which are
not granted `id-token: write`.

## Amendment: the root ships, the default does not change

The decision above said `KeyVerifier` stays the default "until a release ships
an embedded trusted root and a pinned identity". v0.6.0 published a signed
registry, and `sigstore.ReleasePolicy()` now carries the embedded root and the
pinned `release.yml@refs/heads/main` identity — so the stated precondition is
met. The default stays keyed anyway.

The reason is size, measured rather than assumed: a binary that merely imports
`router/verify/sigstore` links to 25.8 MB, against 11.4 MB for the entire CLI.
Making it the default means `open.go` imports it unconditionally, so every host
embedding the router pays roughly +15 MB — including hosts that only use the
in-process `openai-compatible` driver and never install a provider binary at
all. The consequence above argues the dependency should land on the feature
that needs it; making it the default lands it on everyone.

So verification stays opt-in, one call:
`sigstore.NewVerifier(sigstore.ReleasePolicy())`.

The cost of that choice is the one this ADR already named: a host that forgets
to opt in gets no installer, and no installer re-enables unverified `PATH`
lookup. That remains wrong-by-default and unresolved — `ReleasePolicy` only
makes opting in cheap enough that there is no longer an excuse.

## Open

- Can the installer depend on Sigstore verification at *verify* time in every
  deployment we care about? Offline verification against a pinned trusted root
  avoids a network call, but the trusted root still has to be refreshed.
- Silent downgrade on the way in: no trust root configured means no installer,
  which means unsigned `PATH` binaries execute. Opt-in verification cannot fix
  that; only changing what happens when nothing is configured can.
- Refreshing the embedded root is a manual `make sync-trusted-root`. CI reports
  staleness weekly, but nothing forces the refresh before a user meets it.
