.PHONY: test test-race test-submodules build e2e lint lint-ci lint-submodules verify fmt tidy release sync-module-versions check-module-versions

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
