#!/usr/bin/env bash
# Provision the pinned llama.cpp build and GGUF model the E2E suite runs
# against, then print the resolved paths as shell exports.
#
#   eval "$(scripts/provision-e2e-llamacpp.sh)"
#
# Everything is pinned by scripts/e2e-fixtures.env and verified by SHA-256
# before use. Provisioning failure is fatal: the suite must never degrade into
# a skip, because a skipped engine test is indistinguishable from a passing one
# in a green check.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck source=scripts/e2e-fixtures.env
. "$repo_root/scripts/e2e-fixtures.env"

CACHE_DIR=${INFROUTER_LLM_E2E_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/go-inference-router-e2e}
ENGINE_DIR="$CACHE_DIR/llamacpp-$LLAMACPP_RELEASE"
MODEL_DIR="$CACHE_DIR/models/$MODEL_REVISION"
MODEL_PATH="$MODEL_DIR/$MODEL_FILE"

log() { echo "provision-e2e: $*" >&2; }

# verify checks a file against an expected SHA-256, removing it on mismatch so a
# truncated or tampered download cannot be reused by the next run.
verify() {
	local path=$1 expected=$2 actual
	actual=$(sha256sum "$path" | cut -d' ' -f1)
	if [ "$actual" != "$expected" ]; then
		rm -f "$path"
		log "checksum mismatch for $path"
		log "  expected $expected"
		log "  actual   $actual"
		return 1
	fi
}

fetch() {
	local url=$1 dest=$2
	log "downloading $url"
	curl --fail --location --silent --show-error --retry 3 --retry-delay 2 -o "$dest.part" "$url"
	mv "$dest.part" "$dest"
}

mkdir -p "$CACHE_DIR" "$MODEL_DIR"

# --- engine ------------------------------------------------------------------
# The marker is written only after a verified archive has been fully extracted,
# so an interrupted run re-provisions instead of exposing a partial tree.
if [ ! -f "$ENGINE_DIR/.provisioned" ]; then
	archive="$CACHE_DIR/$LLAMACPP_ASSET"
	if [ ! -f "$archive" ] || ! verify "$archive" "$LLAMACPP_SHA256" 2>/dev/null; then
		fetch "https://github.com/ggml-org/llama.cpp/releases/download/$LLAMACPP_RELEASE/$LLAMACPP_ASSET" "$archive"
	fi
	verify "$archive" "$LLAMACPP_SHA256"
	rm -rf "$ENGINE_DIR"
	mkdir -p "$ENGINE_DIR"
	tar -xzf "$archive" -C "$ENGINE_DIR"
	touch "$ENGINE_DIR/.provisioned"
else
	log "engine cache hit: $ENGINE_DIR"
fi

SERVER=$(find "$ENGINE_DIR" -type f -name llama-server -perm -u+x | head -n1)
if [ -z "$SERVER" ]; then
	log "llama-server not found under $ENGINE_DIR"
	exit 1
fi
# The release ships its shared libraries beside the binary; without this the
# server fails to start with an unhelpful loader error.
SERVER_LIB_DIR=$(dirname "$SERVER")

# --- model -------------------------------------------------------------------
if [ ! -f "$MODEL_PATH" ] || ! verify "$MODEL_PATH" "$MODEL_SHA256" 2>/dev/null; then
	fetch "https://huggingface.co/$MODEL_REPO/resolve/$MODEL_REVISION/$MODEL_FILE" "$MODEL_PATH"
fi
verify "$MODEL_PATH" "$MODEL_SHA256"

log "engine  $SERVER"
log "model   $MODEL_PATH"

echo "export INFROUTER_LLM_E2E_LLAMACPP_BIN=$SERVER"
echo "export INFROUTER_LLM_E2E_LLAMACPP_LIB_DIR=$SERVER_LIB_DIR"
echo "export INFROUTER_LLM_E2E_MODEL=$MODEL_PATH"
