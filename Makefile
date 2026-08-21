.PHONY: test test-race test-submodules build e2e sigstore-e2e sync-trusted-root lint lint-ci lint-submodules verify fmt tidy release sync-module-versions check-module-versions

CUSTOM_LINT ?= ./custom-golangci-lint
CUSTOM_LINT_ABS := $(abspath $(CUSTOM_LINT))

test:
	go test ./...

test-race:
	go test -race ./...
	@for m in $(SUBMODULES); do \
		echo "==> $$m (race)"; \
		(cd $$m && go test -race ./...) || exit 1; \
	done

# Submodules live outside the core module so its dependency-free promise holds;
# `go test ./...` therefore does not reach them and CI must run them by name.
SUBMODULES = router provider/anthropicsdk provider/openaisdk provider/bedrocksdk

test-submodules:
	@for m in $(SUBMODULES); do \
		echo "==> $$m"; \
		(cd $$m && go test ./...) || exit 1; \
	done

build:
	mkdir -p .cache/bin
	cd router && go build -o ../.cache/bin/go-inference-router ./cmd/go-inference-router
	cd provider/openaisdk && go build -o ../../.cache/bin/go-inference-router-provider-openai ./cmd/go-inference-router-provider-openai
	cd provider/anthropicsdk && go build -o ../../.cache/bin/go-inference-router-provider-anthropic ./cmd/go-inference-router-provider-anthropic
	cd provider/bedrocksdk && go build -o ../../.cache/bin/go-inference-router-provider-bedrock ./cmd/go-inference-router-provider-bedrock

# e2e runs the driver against a real llama.cpp server and a real model, both
# pinned by scripts/e2e-fixtures.env. Provisioning is part of the test: if it
# fails, the suite fails on the missing fixture rather than skipping, because a
# skipped engine test is indistinguishable from a passing one.
e2e:
	eval "$$(./scripts/provision-e2e-llamacpp.sh)" && \
		go test -tags=e2e ./e2e -count=1 -timeout=15m -v

# Verifies a real cosign bundle with the real verifier. Needs an ambient OIDC
# token, so it runs in CI (.github/workflows/sigstore-e2e.yml) rather than as
# part of `verify`; locally it needs the SIGSTORE_E2E_* fixtures that workflow
# builds. The test fails rather than skips when they are missing — a signing
# test that quietly does not run looks exactly like one that passed.
sigstore-e2e:
	cd router && go test -tags=sigstoree2e -count=1 -timeout=10m -v ./verify/sigstore/...

# The trusted root embedded by sigstore.ReleasePolicy expires when Sigstore
# rotates its own roots, which no code change announces — the weekly Sigstore
# E2E run is what reports it, and this is the fix.
#
# `cosign initialize` fetches the TUF repository and caches trusted_root.json as
# a target. Not `cosign trusted-root create`: that builds a root from material
# passed in flags, and with none emits one with no transparency logs at all.
sync-trusted-root:
	cosign initialize
	cp "$$(find "$$HOME/.sigstore/root" -name trusted_root.json -print -quit)" \
		router/verify/sigstore/trusted_root.json
	cd router && go test -count=1 -run ReleasePolicy ./verify/sigstore/

# lint uses the custom build (which carries the goclocbudget plugin) when it is
# present, and falls back to a stock golangci-lint otherwise — that fallback
# skips the LOC budget, so CI always runs lint-ci.
lint:
	@if [ -x $(CUSTOM_LINT) ]; then \
		$(CUSTOM_LINT) run; \
	else \
		golangci-lint run ./...; \
	fi

custom-golangci-lint: .custom-gcl.yml
	golangci-lint custom

lint-ci: custom-golangci-lint
	$(CUSTOM_LINT) run
	@for m in $(SUBMODULES); do \
		echo "==> $$m (lint)"; \
		(cd $$m && $(CUSTOM_LINT_ABS) run ./...) || exit 1; \
	done

lint-submodules: custom-golangci-lint
	@for m in $(SUBMODULES); do \
		echo "==> $$m (lint)"; \
		(cd $$m && $(CUSTOM_LINT_ABS) run ./...) || exit 1; \
	done

verify: test test-submodules lint-ci

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

tidy:
	go mod tidy
	@for m in $(SUBMODULES); do (cd $$m && go mod tidy); done

release:
	@test -n "$(VERSION)" || (echo "VERSION is required" && exit 1)
	./scripts/build-release.sh "$(VERSION)" "$(OUTDIR)"

# Published submodules require the root module by version rather than by
# replace directive, so they have to be pointed at the version about to be
# tagged before a release runs. go.work keeps local builds on the checkout.
sync-module-versions:
	@test -n "$(VERSION)" || (echo "VERSION is required" && exit 1)
	@for m in $(SUBMODULES); do \
		(cd $$m && go mod edit -require=github.com/rivt-ai/go-inference-router@$(VERSION)) || exit 1; \
		echo "$$m -> $(VERSION)"; \
	done

check-module-versions:
	@test -n "$(VERSION)" || (echo "VERSION is required" && exit 1)
	./scripts/check-module-versions.sh "$(VERSION)"
