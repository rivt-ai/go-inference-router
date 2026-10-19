#!/usr/bin/env bash
# Points every published submodule at root version $1: requires it in go.mod
# and records its hash in go.sum, computed from HEAD by scripts/modsum (the
# root tag does not exist yet). The release commit this produces touches only
# those files, so the root module's content - and hash - is HEAD's.
set -euo pipefail

VERSION=${1:?version is required}
ROOT=github.com/rivt-ai/go-inference-router
cd "$(git rev-parse --show-toplevel)"

sums=$(cd scripts/modsum && GOWORK=off go run . ../.. "$ROOT" "$VERSION" HEAD)

for module in router provider/openaisdk provider/anthropicsdk provider/bedrocksdk; do
	(cd "$module" && go mod edit -require="$ROOT@$VERSION")
	# Swap the root's lines in place so the rest of go.sum keeps its order.
	awk -v sums="$sums" -v root="$ROOT v" '
		index($0, root) == 1 { if (!done) print sums; done = 1; next }
		{ print }
		END { if (!done) print sums }
	' "$module/go.sum" >"$module/go.sum.new"
	mv "$module/go.sum.new" "$module/go.sum"
done

./scripts/check-module-versions.sh "$VERSION"
