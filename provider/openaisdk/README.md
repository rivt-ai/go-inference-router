# openaisdk — OpenAI's Responses API

Drives OpenAI through the official Go SDK
(`github.com/openai/openai-go`), targeting the **Responses** API — not chat
completions. `provider/openaicompat` covers chat completions, for the
self-hosted servers that speak it.

```go
client := openaisdk.New(openaisdk.Config{APIKey: os.Getenv("OPENAI_API_KEY")})
```

## Its own Go module

Like `provider/anthropicsdk`, and for the same reason: the core module carries
no third-party dependencies, so every SDK-backed driver lives outside it.
Importing `github.com/rivt-ai/go-inference-router` never pulls the SDK in.

```
require github.com/rivt-ai/go-inference-router/provider/openaisdk v0.0.0
```

The `replace ../..` in `go.mod` is there because the core module is untagged
pre-1.0. Drop it once the core carries a version.

## Responses is not chat completions renamed

Three structural differences the driver absorbs, so a host sees the same
`llm.Request` either way:

| | Chat completions | Responses |
|---|---|---|
| System prompt | a message with `role: "system"` | top-level `instructions` |
| Tool call | nested in the assistant message | its own top-level input item |
| Tool result | a message with `role: "tool"` | a `function_call_output` item |
| Why it stopped | per-choice `finish_reason` | response-level `status` plus `incomplete_details.reason` |

Consequences worth knowing:

- **System messages are hoisted and joined**, including mid-conversation ones —
  which therefore apply from the start of the exchange rather than from their
  original position.
- **The finish reason is synthesised.** "Stopped to call a tool" is not reported
  by the API; the driver infers it from a `completed` status whose output
  contained a `function_call`.
- **`ToolCall.ID` carries `call_id`, not the item id.** A `function_call_output`
  must reference `call_id`; using the item id is silently wrong on the next
  turn.

## Capabilities

Implements `Streamer`, `Embedder`, and `ModelLister`. It does **not** implement
`MetadataReporter`: the models endpoint reports no context window, so there is
nothing truthful to return. (Anthropic's driver has the mirror-image gap — no
embeddings endpoint.)

## Streaming

The Responses stream is item-oriented and carries a fully-formed `Response` on
completion, so this driver keeps no accumulator at all — it forwards deltas and
returns the API's own final snapshot. Tool calls are emitted when their item
closes, so a caller gets a completed call before the stream ends.
