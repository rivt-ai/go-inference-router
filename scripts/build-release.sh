#!/usr/bin/env bash
set -euo pipefail

VERSION=${1:?version is required}
OUTDIR=${2:-dist}
BASE_URL=${INFROUTER_RELEASE_BASE_URL:-https://github.com/rivt-ai/go-inference-router/releases/download/${VERSION}}
PUBLIC_KEY=${INFROUTER_REGISTRY_PUBLIC_KEY:?base64 Ed25519 public key is required}
SIGNING_KEY=${INFROUTER_REGISTRY_SIGNING_KEY:?path to Ed25519 private key is required}

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
      -ldflags "-X github.com/rivt-ai/go-inference-router/router.registryPublicKey=${PUBLIC_KEY}" \
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
openssl pkeyutl -sign -rawin -inkey "$SIGNING_KEY" -in "$manifest" | base64 -w0 > "$manifest.sig"
