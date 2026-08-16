#!/usr/bin/env bash
# Verifies every published submodule requires the root module at $1, and that
# none of them carry a replace directive for it. Run `make sync-module-versions
# VERSION=vX.Y.Z` to fix a mismatch.
set -euo pipefail

VERSION=${1:?version is required}
ROOT=github.com/rivt-ai/go-inference-router
status=0

for module in router provider/openaisdk provider/anthropicsdk provider/bedrocksdk; do
	required=$(cd "$module" && go mod edit -json | jq -r \
		--arg root "$ROOT" '.Require[]? | select(.Path == $root) | .Version')
	replaced=$(cd "$module" && go mod edit -json | jq -r \
		--arg root "$ROOT" '[.Replace[]? | select(.Old.Path == $root)] | length')

	if [ "$required" != "$VERSION" ]; then
		echo "$module/go.mod requires $ROOT $required, expected $VERSION" >&2
		status=1
	fi
	if [ "$replaced" != "0" ]; then
		echo "$module/go.mod replaces $ROOT; published modules must not" >&2
		status=1
	fi
done

exit "$status"
