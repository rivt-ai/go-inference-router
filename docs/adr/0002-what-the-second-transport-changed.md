# ADR 0002 — What the second transport changed

Status: accepted · Date: 2026-08-10 · Extends ADR 0001

> The hand-written Anthropic driver this record analyses was later replaced by
> an SDK-backed one (ADR 0003). The findings stand — they are about the seam,
> not the transport — and `FormatSchema` remains in the contract.

## Context

ADR 0001 asserted a provider-neutral seam on the evidence of a single driver.
One driver cannot demonstrate neutrality: a contract shaped by one wire format
looks neutral right up until a second format arrives. Anthropic's Messages API
was chosen as that second transport precisely because it disagrees with the
OpenAI-compatible format in structural ways — not in field names.

## What held

Most of the seam survived contact unchanged, including the parts that were
least obviously portable:

- **`Message` with a `ToolCall` slice and string `Arguments`.** Anthropic sends
  tool arguments as a decoded JSON object, not a string. Keeping the seam's
  form as a string still won: the driver hands over the raw bytes with no
  re-marshal, and the streaming form of the same API is itself a string of
  JSON fragments. A decoded `map[string]any` in the seam would have forced a
  parse the host may not want and lost byte fidelity.
- **`Provider` plus optional capability interfaces.** Anthropic has no
  embeddings endpoint. Its driver simply does not implement `Embedder`, which
  is a compile-time fact a host can type-assert on — the alternative, a
  mandatory `Embed` returning "unsupported", would have moved the failure to
  runtime for every caller.
- **Typed `Kind` classification.** Anthropic returns a structured `error.type`,
  so this driver classifies by contract rather than by status code, and 529
  `overloaded_error` lands on `KindUnavailable` where a status-only mapping
  would have guessed.
- **`Request.Extra`.** Identical semantics in both drivers.
- **`ChatStream` returning what `Chat` returns.** Held, and the Messages API
  supports it better: explicit block-stop framing lets this driver deliver a
  completed tool call mid-stream rather than at end-of-stream.

## What did not hold

**`ResponseFormat` was OpenAI's shape wearing a neutral name.** It offered
exactly two values, text and `json_object`, because that is what the
OpenAI-compatible API offers. Anthropic constrains output only by schema —
there is no unconstrained JSON mode to map `FormatJSON` onto.

Decision: add `FormatSchema` with `Request.ResponseSchema` and `SchemaName`,
and make it the portable setting. `FormatJSON` remains for providers that have
that mode; a driver without it returns `KindInvalidRequest` naming the
alternative rather than silently answering in prose. Both drivers now support
schema-constrained output.

The general lesson, recorded because it will recur: a neutral enum whose
members are one vendor's feature list is not neutral. The test is whether a
value can be *refused* by a conforming provider — if it can, it is a
capability, and it needs an explicit failure or an optional interface, not a
silent downgrade.

## Consequences

- `internal/transport` now holds the SSE scanner and the stall guard, which
  both drivers share unchanged. Extracting it rather than having one driver
  embed the other keeps each driver's exported surface its own — the reason
  embedding was rejected when a llama.cpp-specific driver was first considered.
- Two structural translations live entirely inside the Anthropic driver and are
  invisible to hosts: system messages are hoisted into a top-level field (so a
  mid-conversation system message applies from the start of the exchange, which
  the driver documents), and consecutive tool messages are coalesced into a
  single user message, because splitting parallel tool results across messages
  teaches the model to stop making parallel calls.
- Prompt-token accounting is summed from three fields rather than read from
  one. `input_tokens` alone is the uncached remainder; reporting it as
  `PromptTokens` would under-count every cached request.
- `max_tokens` is required by this API and optional in the seam, so the driver
  supplies a documented default. A neutral request that omits an output cap
  stays legal.
