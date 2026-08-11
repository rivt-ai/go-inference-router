# Parallel tool calls across router providers

Status: research complete · Date: 2026-08-10 · Trigger: [PR #6](https://github.com/rivt-ai/go-inference-router/pull/6)

## Question

PR #6 added the tri-state `Request.ParallelToolCalls` field and wired it only
to the in-process OpenAI-compatible Chat Completions driver. Does the router or
any other built-in provider need corresponding work?

## Conclusion

Yes for the OpenAI Responses and Anthropic Messages provider adapters. No
additional router plumbing is required. Amazon Bedrock Converse has no portable
wire-level equivalent, so its adapter should reject a non-nil request rather
than silently ignore the requested behavior.

| Component | Native control | Conclusion |
| --- | --- | --- |
| Router / provider-process protocol | Carries `llm.Request` directly | No mapping change; add a round-trip regression test when implementing the adapters. |
| OpenAI-compatible Chat Completions (`provider/openaicompat`) | Top-level `parallel_tool_calls` boolean | Complete in PR #6; no further work. |
| OpenAI Responses (`provider/openaisdk`) | Top-level `parallel_tool_calls` boolean | Wire both explicit `true` and `false`; preserve nil as absent. |
| Anthropic Messages (`provider/anthropicsdk`) | `tool_choice.disable_parallel_tool_use` | Wire with inverted polarity; preserve nil as absent. |
| Amazon Bedrock Converse (`provider/bedrocksdk`) | None in `ToolConfiguration` | Do not invent a mapping; return `KindInvalidRequest` when the field is non-nil. |

## Evidence

### The router already transports the field

The merged change made `ParallelToolCalls` a `*bool` on the neutral `Request`,
so absent, explicit true, and explicit false remain distinct. The merge commit
also states that only the compatible driver consumes it today.
([merged request change](https://github.com/rivt-ai/go-inference-router/commit/f51d96443e37eec2fbcfc247e0b274a7e6b113f3#diff-b2ad37a17b8f934ccd38b0405a1329e792e356a7855572991972c6267a18181f),
[commit description](https://github.com/rivt-ai/go-inference-router/commit/f51d96443e37eec2fbcfc247e0b274a7e6b113f3))

Both protocol hops embed that same type rather than translating its fields:
`ChatRequest.Request` carries `llm.Request` from the client to the router, and
`ProviderChatRequest.Request` carries it from the router to a provider process.
The provider-process client then forwards the received request unchanged.
([protocol types](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/protocol/llmv1/types.go#L116-L121),
[provider-process request](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/protocol/llmv1/types.go#L204-L208),
[client forwarding](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/router/providerproc/client.go#L118-L128))

Therefore JSON encoding already moves the field through the router. A protocol
round-trip test is worthwhile protection, but no new router model, config, or
branch is needed.

### OpenAI Responses: implement the direct mapping

The official Responses API defines `parallel_tool_calls` as the boolean that
controls whether the model may run tool calls in parallel. The pinned official
Go SDK exposes it as `ResponseNewParams.ParallelToolCalls`, an optional boolean,
so it has exactly the same tri-state representation as the neutral request.
([Responses API reference](https://platform.openai.com/docs/api-reference/responses/create),
[openai-go v1.12.0 field](https://github.com/openai/openai-go/blob/v1.12.0/responses/response.go#L12752))

The current `buildParams` maps tool choice, temperature, top-p, and the other
supported options but never reads `req.ParallelToolCalls`.
([current adapter](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/provider/openaisdk/wire.go#L20-L49))

Recommended mapping:

```go
if req.ParallelToolCalls != nil {
    params.ParallelToolCalls = openai.Bool(*req.ParallelToolCalls)
}
```

Tests should pin all three states: nil omitted, true sent, and false sent.

### Anthropic Messages: implement the inverse nested mapping

Anthropic enables parallel tool use by default. Its control is not top-level:
`disable_parallel_tool_use: true` lives inside `tool_choice`. With `auto`, it
limits a response to at most one tool call; with `any` or a named `tool`, it
requires exactly one. ([Anthropic parallel tool-use documentation](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use#disable-parallel-tool-use))

The pinned official Go SDK exposes the optional boolean on the `auto`, `any`,
and named-tool choice variants. It is absent from the `none` variant.
([auto variant](https://github.com/anthropics/anthropic-sdk-go/blob/v1.62.0/message.go#L6912-L6921),
[any variant](https://github.com/anthropics/anthropic-sdk-go/blob/v1.62.0/message.go#L6934-L6943),
[tool variant](https://github.com/anthropics/anthropic-sdk-go/blob/v1.62.0/message.go#L6979-L6989))

The current adapter constructs those variants without setting the field.
([current mapping](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/provider/anthropicsdk/wire.go#L142-L153))

Recommended semantics:

- nil: leave `DisableParallelToolUse` absent, preserving Anthropic's default;
- neutral false: set `DisableParallelToolUse` to true;
- neutral true: set `DisableParallelToolUse` to false explicitly;
- unset/auto tool choice with tools present: create the `auto` variant when a
  parallel setting must be expressed;
- required tool choice: set the field on the existing `any` variant;
- `ToolChoiceNone` or no tools: the setting has no meaningful effect; keep the
  no-tools behavior rather than constructing an invalid tool-choice object.

Tests should cover the polarity inversion and the synthesized default `auto`
choice, in addition to nil omission.

### Amazon Bedrock Converse: unsupported at the portable API layer

Bedrock's official `ToolConfiguration` shape has only `tools` and
`toolChoice`. Its `ToolChoice` union controls whether the model selects tools
automatically, must select at least one, or must select a named tool; neither
type includes a parallel-call switch.
([ToolConfiguration API](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ToolConfiguration.html),
[ToolChoice API](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ToolChoice.html))

The adapter faithfully builds only those fields today.
([current Bedrock mapping](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/provider/bedrocksdk/wire.go#L162-L181))

This is an inference from the complete public request shape: Converse can
return multiple tool-use blocks, but it offers no provider-neutral guarantee
that enables or disables them. Passing an Anthropic-specific field through
`AdditionalModelRequestFields` would couple this generic Bedrock adapter to one
model family and would not work uniformly for the other Bedrock models.

The repository's seam rule says a provider that cannot express a neutral
capability must fail explicitly rather than silently downgrade it.
([ADR 0002](https://github.com/rivt-ai/go-inference-router/blob/f51d96443e37eec2fbcfc247e0b274a7e6b113f3/docs/adr/0002-what-the-second-transport-changed.md#what-did-not-hold))
Accordingly, `provider/bedrocksdk` should return `KindInvalidRequest` whenever
`ParallelToolCalls != nil`, naming the unsupported control. Nil should retain
the provider/model default.

## Suggested implementation order

1. Add OpenAI Responses mapping and tri-state adapter tests.
2. Add Anthropic's inverse nested mapping and adapter tests.
3. Add Bedrock's explicit unsupported-value error and test.
4. Extend the `llm.v1` JSON round-trip test with explicit false to pin router
   transport, without adding router production code.

There was no existing standalone research-note directory. This note lives in
`docs/research/` to keep evidence gathering separate from the accepted
decisions in `docs/adr/`.
