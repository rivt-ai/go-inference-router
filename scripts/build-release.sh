#!/usr/bin/env bash
set -euo pipefail

VERSION=${1:?version is required}
OUTDIR=${2:-dist}
BASE_URL=${INFROUTER_RELEASE_BASE_URL:-https://github.com/rivt-ai/go-inference-router/releases/download/${VERSION}}
# Both accept a comma-separated list so a release can be signed by more than one
# key at once. That is what a rotation looks like in practice: for one release
# cycle the manifest carries a signature from the outgoing key and the incoming
# key, and the binaries built here trust both. Binaries from before the rotation
# verify against the old signature, binaries after it verify against the new
# one, and the old key can be dropped once no supported build trusts it alone.
PUBLIC_KEY=${INFROUTER_REGISTRY_PUBLIC_KEY:?comma-separated base64 Ed25519 public key(s) required}
SIGNING_KEY=${INFROUTER_REGISTRY_SIGNING_KEY:?comma-separated path(s) to Ed25519 private key(s) required}

mkdir -p "$OUTDIR"
OUTDIR=$(cd "$OUTDIR" && pwd)
TARGETS=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64)
PROVIDERS=(openai anthropic bedrock)

for target in "${TARGETS[@]}"; do
  GOOS=${target%-*}
  GOARCH=${target#*-}
  suffix=""
  if [[ "$GOOS" == windows ]]; then suffix=.exe; fi

  (
    cd router
    env GOOS="$GOOS" GOARCH="$GOARCH" go build \
      -ldflags "-X github.com/rivt-ai/go-inference-router/router.registryPublicKeys=${PUBLIC_KEY}" \
      -o "$OUTDIR/go-inference-router-${target}${suffix}" ./cmd/go-inference-router
  )

  for provider in "${PROVIDERS[@]}"; do
    module="provider/${provider}sdk"
    command="go-inference-router-provider-${provider}"
    (
      cd "$module"
      env GOOS="$GOOS" GOARCH="$GOARCH" go build \
        -ldflags "-X main.version=${VERSION}" \
        -o "$OUTDIR/${command}-${target}${suffix}" "./cmd/${command}"
    )
  done
done

manifest="$OUTDIR/providers.json"
printf '{"version":1,"artifacts":[' > "$manifest"
separator=""
for target in "${TARGETS[@]}"; do
  os=${target%-*}
  arch=${target#*-}
  suffix=""
  if [[ "$os" == windows ]]; then suffix=.exe; fi
  for provider in "${PROVIDERS[@]}"; do
    name="go-inference-router-provider-${provider}-${target}${suffix}"
    size=$(stat -c %s "$OUTDIR/$name")
    digest=$(sha256sum "$OUTDIR/$name" | cut -d ' ' -f 1)
    printf '%s{"provider":"%s","version":"%s","protocol":"llm.v1","os":"%s","arch":"%s","url":"%s/%s","size":%s,"sha256":"%s"}' \
      "$separator" "$provider" "$VERSION" "$os" "$arch" "$BASE_URL" "$name" "$size" "$digest" >> "$manifest"
    separator=,
  done
done
printf ']}' >> "$manifest"
# The sidecar names the key each signature came from, so a verifier checks only
# the signature belonging to a key it holds instead of trying all of them. The
# key ID is derived from the public key (first 4 bytes of its SHA-256, hex) by
# both signer and verifier, so an ID can never name a key other than the one it
# was minted from.
key_id() {
  printf '%s' "$1" | base64 -d | sha256sum | cut -c1-8
}

IFS=',' read -r -a SIGNING_KEYS <<< "$SIGNING_KEY"
IFS=',' read -r -a PUBLIC_KEYS <<< "$PUBLIC_KEY"
if [[ ${#SIGNING_KEYS[@]} -ne ${#PUBLIC_KEYS[@]} ]]; then
  echo "signing and public key lists must be the same length and in the same order" >&2
  exit 1
fi

{
  printf '{"version":1,"signatures":['
  separator=""
  for index in "${!SIGNING_KEYS[@]}"; do
    signing_key=$(echo "${SIGNING_KEYS[$index]}" | xargs)
    public_key=$(echo "${PUBLIC_KEYS[$index]}" | xargs)
    signature=$(openssl pkeyutl -sign -rawin -inkey "$signing_key" -in "$manifest" | base64 -w0)
    printf '%s{"key_id":"%s","signature":"%s"}' "$separator" "$(key_id "$public_key")" "$signature"
    separator=,
  done
  printf ']}'
} > "$manifest.sig"
