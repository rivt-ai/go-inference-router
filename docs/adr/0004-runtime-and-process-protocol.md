# ADR 0004 — Runtime and process protocol

Status: accepted · Date: 2026-08-10 · Extends ADR 0001

## Context

The provider-neutral Go seam needs to support runtime Model Profile
selection, SDK isolation, capability discovery, and providers installed after
the host application ships. Keeping provider adapters as imported Go packages
cannot provide that lifecycle.

## Decision

Expose the dependency-free Go contract as package `llm` in the
`go-inference-router` module.

A host launches one `go-inference-router` child per process. The Router speaks a
versioned `llm.v1` JSON-RPC 2.0 protocol over stdin/stdout and launches optional
`go-inference-router-provider-*` children over the same transport. Requests are
multiplexed by ID, streaming events remain ordered per request, and Provider
Processes advertise their concurrency and Capabilities during initialization.

The host may reload Provider Definitions and Model Profiles through `llm.v1`.
The Router retains unchanged providers, while changed or removed Provider
Definitions cancel active calls and close their processes. Registry changes
require a Router restart. Hosts may also stop one Provider Process explicitly;
the next request opens it again.

Lifecycle, request, install, and SDK retry observations use an optional typed
observer. Hosts opt in during protocol initialization, and Provider Processes
forward correlated observations over `llm.v1` without including request or
secret payloads.

The OpenAI-compatible Provider Definition remains built in. SDK-backed
providers remain separate Go modules and become Provider Processes. Missing
first-party Provider Processes require an explicit install approval, an
Ed25519-verified registry manifest, and an artifact SHA-256 match before atomic
installation.

## Consequences

- The host owns selected-profile state; the Router owns profile resolution and provider lifecycle.
- Only configured Model Profiles are selectable; discovered provider model IDs are informational.
- Live reload preserves unchanged providers and cancels work owned by replaced providers.
- Observation delivery is best effort and must not affect model calls.
- The Go API is pre-1.0 and unreleased; `llm.v1` is additive and backward compatible.
- Host integration lands separately from this repository.
