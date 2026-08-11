# anthropicsdk

`anthropicsdk` adapts Anthropic's official Go SDK to the provider-neutral
`llm.Provider`, `Streamer`, `ModelLister`, and `MetadataReporter` interfaces.
Anthropic has no embeddings endpoint, so the adapter does not implement
`Embedder`.

## Separate Go module

The core module promises zero third-party dependencies. Keeping this adapter in
its own module makes the SDK and its transitive dependencies opt-in:

```text
require github.com/rivt-ai/go-inference-router/provider/anthropicsdk v0.0.0
```

The local `replace ../..` remains until the core module has a released version.

## Behavior

- SDK retries are enabled by default; set `Config.MaxRetries` to `-1` when the
  caller owns retry policy.
- API errors are classified by HTTP status because the SDK does not expose the
  structured Anthropic error type.
- Responses must declare `Content-Type: application/json` for SDK decoding.
- Structured output requires `FormatSchema`; unconstrained JSON is rejected.
- System messages are hoisted, consecutive tool results are coalesced, and the
  API-required `max_tokens` always has a value.
