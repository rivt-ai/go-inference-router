# ADR 0001 — A provider-neutral seam, owned outside the host

Status: accepted, rule 3 superseded by ADR 0003 · Date: 2026-08-09

## Context

The originating host application's LLM access lived in `clients/`, built on
`github.com/openai/openai-go`. The SDK's types were not confined to that
package: `openai.ChatCompletionNewParams` and the message unions flowed through
the runner, so every component that touched a turn was coupled to one vendor's
wire format. That host's own extension strategy records this as the reason a
provider plugin interface was deferred — the coupling has to be paid down
first.

Two further pressures:

- That host's `goclocbudget` caps implementation Go at a fixed size and fails CI
  on size alone. Growing multi-provider support inside the host competes with
  feature work for the same budget.
- The backend seam is useful beyond any single host, and it changes for different reasons
  than the agent loop does.

## Decision

`inference-core` owns the seam as a standalone module.

1. **`inference` defines vendor-neutral types.** `Message`, `ToolCall`, `Tool`,
   `Request`, `Response`, `Usage`, `Event`. No provider SDK type appears in a
   signature, and the package imports nothing outside the standard library
   (in fact, only `context` and `errors`).

2. **`Provider` is a floor; capabilities are optional interfaces.** Streaming,
   embeddings, model listing, and metadata are separate interfaces a driver may
   also satisfy. Hosts type-assert for what they need, so a minimal backend is
   still a legal backend instead of a pile of `ErrUnsupported`.

3. **Drivers are hand-written over `net/http`.** *(Superseded by ADR 0003:
   SDK-backed drivers are now the default, isolated in their own Go modules so
   the core module's dependency-free guarantee — the point of this rule —
   still holds.)* No vendor SDK is vendored. The
   OpenAI-compatible wire format is small and stable; owning the JSON is
   cheaper than inheriting an SDK's type system, and it is what keeps rule 1
   enforceable rather than aspirational.

4. **Failures are typed, never text.** Every driver error is an
   `*llm.Error` with a `Kind`. Cases the host must branch on —
   a provider failing to parse the model's own tool-call arguments, a stalled
   stream, cancellation — are kinds, not substrings. Where a provider offers no
   structured signal (llama.cpp's tool-call parse failure), the substring match
   is confined to the driver and converted to a kind at the boundary.

5. **Vendor extras go in `Request.Extra`,** merged into the request body at the
   top level, with declared fields winning collisions. One neutral struct plus
   an escape hatch, rather than a union of every vendor's parameters.

6. **Streaming and non-streaming return the same thing.** `ChatStream`
   accumulates and returns the `*Response` that `Chat` would have returned, so
   incremental rendering is a delivery detail, not a second code path.

## Consequences

- Adding a backend means adding a driver package, not touching the host.
- A host can adopt this behind its existing `clients.LLMProvider` interface,
  translating at one boundary, and delete its SDK dependency once its runner
  stops passing `openai.*` types around. That adoption is deliberately *not*
  part of this repository's first cut.
- Hand-written drivers mean tracking wire-format changes ourselves. Accepted:
  the covered surface (chat completions, embeddings, models) changes slowly,
  and the tests pin the shapes we depend on.
- A `goclocbudget` of 3,000 lines applies here too. Breadth is meant to arrive
  as new drivers; if the seam itself needs the budget raised, that is a signal
  to re-examine the abstraction first.
