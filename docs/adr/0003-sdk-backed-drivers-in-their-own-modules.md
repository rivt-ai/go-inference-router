# ADR 0003 — SDK-backed drivers, isolated in their own modules

Status: accepted · Date: 2026-08-10 · Supersedes rule 3 of ADR 0001

## Context

ADR 0001 rule 3 said drivers are hand-written over `net/http` and no vendor SDK
is vendored. The reasoning was sound but conflated two things: *the core module
must not carry vendor dependencies* (the actual requirement) and *therefore no
driver may use an SDK* (a consequence that only holds for a single-module
repository).

Writing the Anthropic driver twice — once by hand, once on the official SDK —
made the cost of the hand-written path concrete. The SDK carries retries, stream
accumulation, pagination, and wire-format drift; the hand-written driver carries
all of that as code we maintain and as a standing obligation to track an API we
do not control.

## Decision

**Each provider is driven by its vendor's official SDK, and every SDK-backed
driver lives in its own Go module under `provider/`.**

The three drivers, each speaking a genuinely different API:

```
                 Agent harness
                       │
                llm.Provider
                       │
      ┌────────────────┼────────────────┐
      │                │                │
  openaisdk       anthropicsdk      openaicompat
  Responses         Messages       ChatCompletion
                                        │
                             llama.cpp / vLLM /
                             Ollama / LM Studio
```

1. **`provider/openaisdk`** — OpenAI's **Responses** API via
   `github.com/openai/openai-go`. Its own module.
2. **`provider/anthropicsdk`** — Anthropic's **Messages** API via
   `github.com/anthropics/anthropic-sdk-go`. Its own module.
3. **`provider/openaicompat`** — the **chat-completions** wire format over
   `net/http`, in the core module with no dependencies. This one stays
   hand-written on purpose: its job is the long tail of self-hosted servers
   (llama.cpp, vLLM, Ollama, LM Studio), which implement the format partially
   and inconsistently. Tolerating that — absent `/models`, llama.cpp's `/props`,
   GBNF grammars through `Request.Extra` — is the feature, and no vendor SDK
   models it.

The core module's `go.mod` stays empty. Importing
`github.com/antonikliment/invokr-llm` gets the seam and the compat driver
with zero third-party dependencies; an SDK arrives only if you import that
driver's module explicitly.

## Consequences

- **The dependency-free guarantee survives, and is now load-bearing rather than
  incidental.** It is enforced by module boundaries instead of by a rule against
  using SDKs.
- **Three APIs, not three transports.** Each driver translates a materially
  different wire format onto the seam, which is what makes the seam's neutrality
  testable rather than asserted. Responses is not chat-completions renamed: the
  system prompt is top-level `instructions`, tool calls and their results are
  flat top-level items rather than message content, and there is no per-choice
  finish reason — it is synthesised from the response status, the incompleteness
  reason, and whether the output contained a call.
- **Each driver's capability gaps differ, and the optional interfaces carry
  that.** Anthropic has no embeddings endpoint, so `anthropicsdk` does not
  implement `Embedder`. OpenAI's models endpoint reports no context window, so
  `openaisdk` does not implement `MetadataReporter`. `openaicompat` implements
  both, because a llama.cpp server can answer both. A host type-asserts and
  routes accordingly; nothing returns "unsupported" at runtime.
- **`make test` no longer covers everything.** Submodules are outside
  `go test ./...`, so `make test-submodules` runs them by name and CI runs both.
- **The submodules carry `replace ../..` while the core is untagged.** Drop it
  once the core module has a version.
- **Upstream owns drift for two of the three.** When an API grows a field, two
  drivers get it on a version bump. `openaicompat` remains ours to maintain,
  which is the right trade for a driver whose value is tolerating servers that
  do not follow the spec.
