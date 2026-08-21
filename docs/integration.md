# Integrating go-inference-router into your agent

This project is host-agnostic; nothing below assumes a particular host. Pick the integration mode that matches how much lifecycle you want
to own.

---

## Mode 1 — Library

Use when your agent knows its providers at compile time and you want the
smallest possible surface.

```go
import (
    llm "github.com/rivt-ai/go-inference-router"
    "github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

client := openaicompat.New(openaicompat.Config{
    BaseURL: "http://localhost:8080",
    APIKey:  os.Getenv("MY_API_KEY"),
})

resp, err := client.Chat(ctx, llm.Request{
    Model:    "local-model",
    Messages: []llm.Message{llm.UserMessage("Hello")},
})
```

Streaming is the same call shape, with a handler:

```go
resp, err := client.ChatStream(ctx, req, func(e llm.Event) error {
    switch e.Kind {
    case llm.EventContent:   fmt.Print(e.Text)
    case llm.EventReasoning: // optional: render or ignore
    case llm.EventToolCall:  // incremental tool-call deltas
    }
    return nil
})
```

`ChatStream` returns the same `*llm.Response` that `Chat` would have — streaming
is a delivery detail, so your agent loop has one code path.

**Cost:** `openaicompat` is in the root module and pulls in no third-party
dependencies. `openaisdk`, `anthropicsdk`, and `bedrocksdk` are separate Go
modules; importing one brings its vendor SDK's dependency tree with it.

### Handling errors

```go
if err != nil {
    switch {
    case llm.IsKind(err, llm.KindAuth):           // fix credentials, do not retry
    case llm.IsKind(err, llm.KindToolCallParse):  // feed back as a tool result
    case llm.IsKind(err, llm.KindEmptyResponse):  // provider generated nothing
    case llm.Retryable(err):                      // rate_limit/unavailable/transport/stalled
    default:
    }
}
```

Never match on error text. `Kind` is the stable contract.

`KindEmptyResponse` means a well-formed exchange carried no generation — no
choices, or a stream that ended without a chunk. It is deliberately separate
from `KindProtocol`, which means the provider sent something unreadable. Hosts
usually want to nudge and re-ask on the first and fail loudly on the second.

### Backing off

`Retryable(err)` reports whether retrying *may* help; it does not decide
whether retrying is *safe*. A host that must not pay for a second generation,
or that has already streamed part of a response to a user, should narrow that
set itself.

When a provider says how long to wait, the delay is on the error:

```go
var typed *llm.Error
if errors.As(err, &typed) && typed.RetryAfter > 0 {
    wait(typed.RetryAfter) // provider asked for this
}
```

`RetryAfter` is zero when the provider sent no hint, in which case a caller
should fall back to its own backoff schedule.

### Probing what a provider can do

```go
if s, ok := provider.(llm.Streamer); ok        { /* stream */ }
if e, ok := provider.(llm.Embedder); ok        { /* embed */ }
if l, ok := provider.(llm.ModelLister); ok     { /* list models */ }
if c, ok := provider.(llm.CapabilityReporter); ok { /* ask, don't guess */ }
```

---

## Mode 2 — Embedded runtime

Use when your agent wants configured Model Profiles, lazy provider startup,
credential references, and live reload, but you would rather not manage a child
process.

`router.Open` wires configuration, the provider installer, and the registry trust
policy with defaults. `router.New` remains available for hosts supplying their
own `Source`.

**If your host already has a configuration format, pass the config directly.**
This is the recommended shape:

```go
import (
    "github.com/rivt-ai/go-inference-router/router"
    "github.com/rivt-ai/go-inference-router/router/config"
)

r, err := router.Open(ctx, router.Options{
    Config:   &cfg,        // built from your own settings
    Observer: myObserver,
})
defer r.Close()

resp, err := r.Chat(ctx, "sonnet", llm.Request{
    Messages: []llm.Message{llm.UserMessage("Hello")},
}, nil) // nil handler = non-streaming
```

No configuration file is read, so this costs **no YAML dependency**, adds no
second config file to your users' machines, and introduces no second precedence
chain to disagree with yours. It also means no workspace file is read, so a
cloned repository has no configuration it can plant.

**To read the YAML files instead**, supply a loader — a separate package, so the
parser is not linked into hosts that never call it:

```go
r, err := router.Open(ctx, router.Options{
    Loader:  configfile.Loader(workspaceDir, ""),
    Secrets: resolver,   // see below
})
```

Reading a workspace file trusts whatever a repository ships. Prefer `Config`
unless your host gates that trust itself.

**Keyless registry verification** is opt-in, and deliberately not defaulted for
the same reason as the credential resolver below — it costs `sigstore-go` and its
transitive dependencies, and a host keeping the Ed25519 trust root should not pay
for them:

```go
import "github.com/rivt-ai/go-inference-router/router/verify/sigstore"

verifier, err := sigstore.NewVerifier(sigstore.Policy{
    Issuer:   "https://token.actions.githubusercontent.com",
    Identity: "https://github.com/rivt-ai/go-inference-router" +
        "/.github/workflows/release.yml@refs/heads/main",
    TrustedRootJSON: trustedRoot, // embedded, not fetched
})

r, err := router.Open(ctx, router.Options{
    Config:           &cfg,
    RegistryVerifier: verifier,
})
```

The manifest is then authenticated by *who published it* — the release workflow's
OIDC identity, recorded in a public transparency log — rather than by a key you
have to hold and rotate. Leaving `RegistryVerifier` nil keeps the compiled-in
Ed25519 key, which is still the default and still what release builds publish
alongside the bundle.

Both fields of `Policy` are required. A bundle that verifies cryptographically
but names no identity proves only that *somebody* signed it, since anyone can
obtain a certificate; pin the workflow **and its ref**. See ADR 0007.

`TrustedRootJSON` is the Sigstore trust anchor, and the verifier never fetches
it: supplying it is what makes the trust decision yours rather than a network
lookup's. Obtain the public-good root once, at build time, and embed the bytes:

```sh
cosign initialize   # fetches the TUF repository into ~/.sigstore
find ~/.sigstore/root -name trusted_root.json -exec cp {} trusted_root.json \;
```

```go
//go:embed trusted_root.json
var trustedRoot []byte
```

Do **not** reach for `cosign trusted-root create`. Despite the name it does not
fetch anything — it *builds* a root out of material passed in its `--fulcio`,
`--rekor` and `--ctfe` flags, and with no flags emits a root containing no
transparency logs and no certificate authorities at all. Verification then fails
with `not enough verified log entries from transparency log: 0 < 1`. It fails
closed, so nothing is trusted that should not be, but the message does not point
at its cause.

The root is dated material: it stops verifying when Sigstore rotates its keys,
which no code change announces. Refresh the embedded copy on a schedule rather
than at the point a release stops installing.

**Credential references** need a resolver, and it is deliberately not defaulted:

```go
resolver, err := secret.DefaultResolver()   // opts in to age, go-keyring, dbus
```

Calling that is the moment you accept those dependencies. Leave `Secrets` nil if
your configuration has no `secrets:` references, or implement
`router.SecretResolver` yourself — a single `Resolve` method — to keep them out.

The profile ID (`"sonnet"`) is the only model identifier your agent needs to
know. `Router.Chat` fills in `request.Model` from the profile and merges the
profile's `options` into `request.Extra`.

A host that picks models at runtime sets `request.Model` itself: a non-empty
value overrides the profile's pinned model, while the profile keeps supplying
the provider, options, and secrets. `Discover` results are usable as override
values. This replaces the old workaround of synthesizing a profile per model
and calling `Apply` on every switch.

Useful surface:

| Call | Purpose |
|---|---|
| `Profiles(ctx)` | selectable profiles + availability, for a model picker |
| `Capabilities(ctx, profileID)` | features and context/output limits |
| `Status(ctx)` | per-provider lifecycle state |
| `Discover(ctx, providerID)` | raw model IDs (informational only) |
| `Apply(ctx, cfg)` | hot-swap config; unchanged providers keep running |
| `StopProvider(ctx, id)` | stop one provider; next request reopens it |
| `Close()` | stop everything |
| `Installer()` | the provider installer, or nil when no trust root is configured |
| `Reload(ctx)` | re-read the configuration `Loader` supplied |

**What this mode costs.** The `router` package itself pulls no third-party
dependencies beyond `golang.org/x/mod/semver`. YAML arrives only with
`router/configfile`, and age, go-keyring, and dbus only with `router/secret`. A host that supplies its own config and its
own secret resolution links neither.

---

## Mode 3 — Runtime process

Use when your host is not Go, or when you want zero dependencies and full
process isolation of provider SDKs.

Launch `go-inference-router` and speak newline-delimited JSON-RPC 2.0 on its
stdin/stdout. Every line is one JSON object.

```jsonc
// →
{"jsonrpc":"2.0","id":1,"method":"llm.v1.initialize",
 "params":{"client_name":"my-agent","protocols":["llm.v1"],"observations":true}}
// ←
{"jsonrpc":"2.0","id":1,"result":{"protocol":"llm.v1","name":"go-inference-router","version":"dev"}}

// →
{"jsonrpc":"2.0","id":2,"method":"llm.v1.chat",
 "params":{"profile_id":"sonnet","stream":true,
           "request":{"messages":[{"role":"user","content":"Hello"}]}}}
// ← (notifications, ordered, sequence starts at 1)
{"jsonrpc":"2.0","method":"llm.v1.stream.event",
 "params":{"request_id":"2","sequence":1,"event":{"kind":"content","text":"He"}}}
// ← (final result, same accumulated response a non-streaming call returns)
{"jsonrpc":"2.0","id":2,"result":{"response":{"message":{"role":"assistant","content":"Hello!"},
                                              "finish_reason":"stop","usage":{"total_tokens":12}}}}
```

Rules that matter on this transport:

- **Stdout is protocol only.** Logs go to stderr.
- **Cancel with a notification**, not by closing the pipe:
  `{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":"2"}}`.
- **Stream events are per-request and strictly sequential.** A gap is a protocol
  error, not something to paper over.
- **Errors arrive as JSON-RPC errors with code `-32000`** and a structured
  `data` payload: `{"kind":"rate_limit","provider":"openai","status":429,"message":"..."}`.
  Branch on `kind`.
- **Shut down with `llm.v1.shutdown`**, then close stdin.

Method reference: [`../protocol/llmv1/types.go`](../protocol/llmv1/types.go).

---

## Mode 4 — Your own provider

Two extension points, depending on whether you want a process or not.

### An in-process provider: implement `router.Source`

`router/config` holds only plain types and depends on nothing outside the
standard library, so implementing a `Source` — for an internal gateway, or a
fake in tests — does not drag in a YAML parser.

Keeps profile resolution, cancellation, capability inference, concurrency, and
observation, while you decide where providers come from — an internal gateway, a
fake for tests, something already held in memory.

```go
type mySource struct{ providers map[string]llm.Provider }

func (s mySource) Available(_ context.Context, id string, _ config.Provider) bool {
    _, ok := s.providers[id]
    return ok
}

func (s mySource) Open(_ context.Context, id string, _ config.Provider) (llm.Provider, error) {
    p, ok := s.providers[id]
    if !ok {
        return nil, &llm.Error{Kind: llm.KindUnavailable, Provider: id, Message: "not configured"}
    }
    return p, nil
}

r, err := router.New(cfg, mySource{...}, observer)
```

If the returned provider implements `io.Closer`, the Router closes it on
retirement.

### A Provider Process: use `protocol/providerhost`

```go
package main

func main() { providerhost.Main("my-provider", version, factory) }

func factory(ctx context.Context, req llmv1.ProviderInitializeRequest, obs llm.Observer,
) (llm.Provider, llm.Capabilities, error) {
    var baseURL string
    if err := providerhost.Config(req.Config, map[string]*string{"base_url": &baseURL}); err != nil {
        return nil, llm.Capabilities{}, err
    }
    client := myprovider.New(myprovider.Config{
        Name:     req.ProviderID,
        APIKey:   req.Secrets["api_key"],
        BaseURL:  baseURL,
        Observer: obs,
    })
    return client, llm.Capabilities{
        Streaming: true, Tools: true, StructuredOutput: true,
        InputModalities: []llm.Modality{llm.ModalityText},
        MaxConcurrency:  8,
    }, nil
}
```

Point config at it and the Router launches it like any first-party provider:

```yaml
providers:
  mine:
    type: mine
    path: /usr/local/bin/my-provider   # or leave to PATH / registry lookup
    base_url: https://gateway.internal
    secrets:
      api_key: {env: MY_API_KEY}
models:
  house-model:
    provider: mine
    model: internal-v3
```

Declare `MaxConcurrency` honestly — the Router uses it as back-pressure, and
overstating it just moves the queue into your process. Declare capabilities
honestly too: an unsupported request should fail with `KindInvalidRequest`
rather than being silently downgraded.

---

## Checklist for a new host

- [ ] Address models by profile ID; never hardcode a vendor model string.
- [ ] Branch on `llm.Kind`, not on error text.
- [ ] Type-assert optional interfaces (or call `capabilities.get`) before using
      streaming, embeddings, or tools.
- [ ] Pass a real `context.Context` and cancel it — cancellation propagates all
      the way to the HTTP request.
- [ ] Keep secrets as references in config; never inline them.
- [ ] Show install plans to a human before approving them.
- [ ] Treat observations as best-effort telemetry, never as control flow.

See [`architecture.md`](architecture.md) for why each of these is the way it is.
