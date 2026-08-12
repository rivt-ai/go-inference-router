# go-inference-router

One model contract for Go agents, and a runtime that routes it to any provider.

Your host application asks for a **Model Profile** — a friendly ID like `sonnet`
or `local-coder` that a user configured in YAML. go-inference-router resolves
that profile and dispatches the call to the built-in OpenAI-compatible adapter
or to an isolated Provider Process. Your code never learns which provider
answered.

```text
host application (any Go agent, CLI, editor, service)
  └─ Go import, or llm.v1 JSON-RPC over stdio
       └─ go-inference-router
            ├─ OpenAI-compatible (built in, no extra dependencies)
            ├─ go-inference-router-provider-openai
            ├─ go-inference-router-provider-anthropic
            ├─ go-inference-router-provider-bedrock
            └─ your own Provider Process
```

Nothing here is host-specific: the host-facing
surface is a plain Go interface or a JSON-RPC protocol over stdio, so any agent
— Go or otherwise — can drive it.

Why it is split up: the root Go package is the **dependency-free contract**.
YAML, encryption, keyring, process management, and vendor SDKs live in separate
Go modules, so importing the contract costs you nothing.

## Install

```text
go get github.com/rivt-ai/go-inference-router          # the contract + built-in adapter
go install github.com/rivt-ai/go-inference-router/router/cmd/go-inference-router@latest
```

The root package is named `inference`. Examples in this repo import it as
`inference` or alias it to `router`; either reads fine.

## Quickstart

Talk to anything speaking the OpenAI chat-completions wire format — OpenAI,
llama.cpp, vLLM, Ollama, OpenRouter — with no routing and no config file:

```go
import (
    inference "github.com/rivt-ai/go-inference-router"
    "github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

client := openaicompat.New(openaicompat.Config{
    Name:    "local",
    BaseURL: "http://localhost:8080/v1",
})

resp, err := client.ChatStream(ctx, inference.Request{
    Model:    "local-model",
    Messages: []inference.Message{inference.UserMessage("Say hello.")},
}, func(event inference.Event) error {
    if event.Kind == inference.EventContent {
        fmt.Print(event.Text)
    }
    return nil
})
```

`ChatStream` returns the same `*inference.Response` that `Chat` would have, so
streaming and non-streaming callers read the result identically.

Every failure is an `*inference.Error` with a stable `Kind`. Branch on
`inference.IsKind(err, inference.KindAuth)` and `inference.Retryable(err)`
rather than matching error text.

## Which integration mode do I want?

| You want | Use | Cost |
|---|---|---|
| One endpoint, no user configuration | the contract + `openaicompat` | no third-party dependencies |
| User-configured profiles inside your process | embed the `router` module | YAML, secrets, process management |
| Isolation from Go and from vendor SDKs | launch the Router, speak `llm.v1` | one child process |
| Your own model source | implement `router.Source` or a Provider Process | your code only |

The [integration guide](docs/integration.md) walks through all four in order,
and ends with a checklist for a new host.

## Runnable examples

Each is one small, hermetic `main.go` — no network, no API key, no installed
provider binaries.

```text
go run ./examples/library-chat          # smallest useful call
go run ./examples/library-streaming     # streaming events
go run ./examples/tool-calling          # tools and tool results
go run ./examples/capability-probing    # ask before you send
go run ./examples/typed-errors          # Kind-based error handling
go run ./examples/custom-provider       # supply your own provider
cd examples/embedded-router && go run . # profiles resolved in-process
```

**More documentation:** [architecture](docs/architecture.md) ·
[integration guide](docs/integration.md) · [diagrams](docs/diagrams/) ·
[decision records](docs/adr/) · [glossary](CONTEXT.md)

## Model configuration

The Router loads the user file from the OS config directory
(`go-inference-router/models.yaml`), then replaces same-named definitions with
workspace `.go-inference-router/models.yaml` entries. `--config path` loads only
that file.

```yaml
version: 1
providers:
  openai:
    type: openai
    secrets:
      api_key: {env: OPENAI_API_KEY}

  anthropic:
    type: anthropic
    secrets:
      api_key: {keychain: anthropic-api-key}

  local:
    type: openai-compatible
    base_url: http://localhost:8080/v1

models:
  gpt:
    provider: openai
    model: gpt-5
  sonnet:
    provider: anthropic
    model: claude-sonnet
  local-coder:
    provider: local
    model: qwen-coder
```

Only the keys under `models` are selectable Model Profile IDs — `sonnet`, not
`claude-sonnet`. Provider model discovery is informational until a user assigns
a friendly profile ID. Provider-specific settings belong under `options`;
credential references belong under `secrets` and are scoped to the selected
Provider Process. See [`examples/models.yaml`](examples/models.yaml) for every
built-in provider form.

### Secrets

Plaintext YAML secrets are rejected. A secret reference must use exactly one of:

- `env`: environment variable
- `keychain`: OS keychain/keyring account under service `go-inference-router`
- `secret`: age-encrypted local store entry
- `file`: regular owner-only file

Manage encrypted entries without ever printing their values:

```text
printf %s "$TOKEN" | go-inference-router secret set anthropic-api-key
go-inference-router secret list
go-inference-router secret delete anthropic-api-key
```

The encrypted store key comes from `INFROUTER_SECRET_STORE_KEY` or the OS
keychain. On headless Linux systems with no keychain service, the Router creates
an owner-only `secrets.key` beside `secrets.age`. It will not create a new
fallback key for an existing encrypted store.

## Providers

| Provider | Process | Stream | Tools | Schema | Images | Embeddings |
|---|---|:---:|:---:|:---:|:---:|:---:|
| OpenAI-compatible | built in | ✅ | ✅ | ✅ | — | ✅ |
| OpenAI Responses | `go-inference-router-provider-openai` | ✅ | ✅ | ✅ | — | ✅ |
| Anthropic Messages | `go-inference-router-provider-anthropic` | ✅ | ✅ | ✅ | — | — |
| Amazon Bedrock Converse | `go-inference-router-provider-bedrock` | — | ✅ | ✅ | ✅ | — |

Capability gaps are explicit and queryable. Unsupported behavior returns
`KindInvalidRequest`; it is never silently downgraded.

## The `llm.v1` process protocol

A host launches one Router child and speaks newline-delimited JSON-RPC 2.0 over
stdin/stdout; Provider Processes use the same transport. Calls are multiplexed
by request ID. A Provider Process advertises its maximum concurrency and the
Router queues work above that limit. Provider stdout is reserved for protocol
messages, stderr for logs.

`llm.v1` covers:

- initialization and version negotiation
- profile listing, provider status, discovery, metadata, and capabilities
- chat with ordered streaming events, tools, multimodal blocks, and JSON Schema
- embeddings, typed errors, cancellation, and graceful shutdown
- live configuration reload and explicit Provider Process stop
- opt-in typed lifecycle, request, install, and SDK retry observations
- signed install discovery, planning, approval, and exact-version removal

Reloading configuration keeps unchanged providers running. Changed or removed
Provider Definitions cancel their active calls and close their processes;
profile-only changes reuse the existing provider. Registry configuration still
requires a Router restart.

### Installing providers is never implicit

Missing first-party providers are never executed behind your back. The Router
returns an install plan, then waits for a separate approval call. It verifies an
Ed25519-signed registry, artifact size, and SHA-256 before an atomic install in
the user cache, records the digest, and verifies it again immediately before
execution.

Release builds embed the registry public key; private registries may set
`registry.url` and `registry.public_key` in YAML. When a trusted registry key is
configured, unmanaged `go-inference-router-provider-*` binaries found on `PATH`
are disabled unless `registry.allow_path_lookup: true` is set explicitly.

Implicit installs and launches select the newest stable semantic version;
prereleases and legacy version identifiers require an exact version. Hosts can
query `llm.v1.install.available` without creating an approval plan, then reclaim
old cached versions with `llm.v1.install.remove`. Removal refuses versions
pinned by configuration and stops unpinned processes of the same type before
deleting their cache entry. Retention count is host policy.

## Development

```text
make test             # dependency-free core and compatible adapter
make test-submodules  # Router plus all SDK Provider Processes
make test-race
make build
make lint
make verify
make e2e              # pinned llama.cpp and real model
```

Build signed release assets with `make release VERSION=vX.Y.Z`. It requires
`INFROUTER_REGISTRY_PUBLIC_KEY`, `INFROUTER_REGISTRY_SIGNING_KEY`, and
optionally `INFROUTER_RELEASE_BASE_URL`.

`go.work` puts every module in one workspace, so local builds and tests use the
checkout. Published submodules still require the root module *by version* — a
dependency's `replace` directives are ignored by whoever imports it — so their
`go.mod` has to name the version being released.

### Releasing

The Go module proxy resolves each submodule from a tag prefixed with its
directory (`router/v0.2.0`), and a submodule's `go.sum` can only record the
root module's hash once the root tag exists. So a version goes out in two
passes:

1. `make sync-module-versions VERSION=vX.Y.Z`, commit.
2. Run the **Release** workflow — builds the signed assets and tags the root
   module. `make check-module-versions VERSION=vX.Y.Z` gates it.
3. `make tidy`, commit — records the now-published root module's hash in each
   submodule's `go.sum`.
4. Run the **Release Go modules** workflow — verifies each submodule builds
   with `GOWORK=off` (as a consumer sees it), then creates the per-directory
   tags.

## Status

Pre-1.0 and unreleased. The Go API may still change without notice. The
additive `llm.v1` process protocol is the compatibility commitment — pin to it
if you want stability today.
