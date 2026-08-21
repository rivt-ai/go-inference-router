# Migrating a host to v0.7.0

This is a walkthrough for a host that already embeds the router and wants to
collect what v0.7.0 added: error causes in `Error()`, an opt-in `RetryPolicy`,
and a per-request model override. It assumes you know which integration mode
you are in; if you do not, read [`integration.md`](integration.md) first — this
document deliberately does not repeat it.

The material here comes from one real migration: [invokr](https://github.com/antonikliment/invokr),
a Go CLI agent in embedded-runtime mode. Every deletion below is a deletion
that host actually made, and the two things that did *not* migrate are gaps it
actually hit.

---

## 0. Bump — and expect the submodule tags to lag

The SDK-backed drivers (`provider/openaisdk`, `anthropicsdk`, `bedrocksdk`) are
[separate Go modules](adr/0003-sdk-backed-drivers-in-their-own-modules.md), so
they carry their own tags — `router/v0.7.0`, `provider/openaisdk/v0.7.0` — and
each is indexed by the module proxy independently of the root `v0.7.0`. A tag
pushed minutes ago exists on GitHub and still fails to resolve:

```sh
go get github.com/rivt-ai/go-inference-router@v0.7.0        # fine
go get github.com/rivt-ai/go-inference-router/router@v0.7.0 # 404 until indexed
```

Check what the proxy has actually seen rather than what the repository has:

```sh
go list -m -versions github.com/rivt-ai/go-inference-router/router
curl -s https://proxy.golang.org/github.com/rivt-ai/go-inference-router/router/@v/list
```

A mixed set of versions across submodules builds and tests green — the domain
types live in the root module and the submodules depend on it, not on each
other — so bumping the root first and the submodules when they resolve is a
supported intermediate state, not a broken one. invokr shipped exactly that:
root at v0.7.0, router submodule still at v0.5.0 for a day.

---

## 1. Delete your cause-rewording

Before v0.7.0, `Error.Error()` printed kind, provider, status and message but
not the wrapped cause, so a transport failure printed as a bare `transport`.
Hosts compensated by unwrapping and re-formatting:

```go
// Before — no longer needed.
switch provider.Kind {
case llm.KindProtocol:
    if cause := provider.Err; cause != nil {
        return fmt.Errorf("provider protocol failure: %s: %v: %w", provider.Message, cause, err)
    }
case llm.KindTransport:
    if cause := provider.Err; cause != nil {
        return fmt.Errorf("provider transport failure: %v: %w", cause, err)
    }
}
```

`Error()` now folds the cause in itself, and only when it adds information —
a cause already contained in `Message` is not repeated. Delete every such case.

What is worth *keeping* is the wording that carries host-specific operator
guidance rather than restating the cause. invokr kept two:

```go
// After — only the kinds where this host has something to say.
switch provider.Kind {
case llm.KindStalled:
    return fmt.Errorf("provider stalled: %s: %w", provider.Message, err)
case llm.KindEmptyResponse:
    return fmt.Errorf(
        "provider stream ended without any completion chunks (malformed or empty response body): %w", err)
}
return err
```

Note the `%w` in both: the typed `*llm.Error` stays in the chain, so callers
still classify with `llm.IsKind` and never by text. Rewording that drops the
wrap is a bug, not a style choice.

---

## 2. Replace your backoff loop with `RetryPolicy`

Most hosts wrote the same loop: exponential doubling, a cap, prefer the
provider's `Retry-After`, stop on non-retryable, stop on cancellation. That is
now `inference.RetryPolicy` ([`retry.go`](../retry.go)) with the same defaults —
500ms base doubling per retry, 30s cap, a larger `RetryAfter` hint wins over
the schedule, and `KindStalled` excluded unless you opt in with `RetryStalled`.

```go
policy := llm.RetryPolicy{MaxAttempts: maxRetries + 1, BaseDelay: time.Second}
err := policy.Do(ctx, func(ctx context.Context) error {
    var callErr error
    resp, callErr = call(ctx)
    return callErr
})
```

`MaxAttempts` counts the *first* try, so a host that stored "extra retries"
passes `n + 1`. `Do` returns `fn`'s last error unchanged, so your existing
error normalization stays where it is.

The policy is opt-in and the router itself never retries: `Retryable` says a
retry *may* help, not that it is *safe*. That distinction is the whole reason
the next section exists.

### The veto recipe

`RetryPolicy` decides retryability from the error's `Kind`. A host that must
stop retrying for a reason the Kind cannot express — most commonly, part of the
response has already been streamed to a user, so re-sending would duplicate
visible output and pay for a second generation — expresses that by returning an
error whose Kind is *not visible* to the policy:

```go
// retryVeto hides the typed provider error from Kind classification, so a
// failure that is no longer safe to re-issue stops the policy immediately.
type retryVeto struct{ err error }

func (v retryVeto) Error() string { return v.err.Error() }
```

Deliberately no `Unwrap`. `KindOf` uses `errors.As` to find the `*llm.Error`; a
wrapper that unwraps would leak the Kind straight back through and the policy
would retry anyway. Then unwrap it yourself once `Do` returns, so callers see
the real error:

```go
err := policy.Do(ctx, func(ctx context.Context) error {
    var callErr error
    resp, callErr = call(ctx)
    if callErr != nil && safeToRetry != nil && !safeToRetry() {
        return retryVeto{callErr}
    }
    return callErr
})
if err != nil {
    var veto retryVeto
    if errors.As(err, &veto) {
        err = veto.err
    }
    return nil, normalizeProviderError(err)
}
```

`safeToRetry` is whatever your host knows and the router cannot: "have we
emitted a token yet on this turn". The alternative — a callback field on the
policy — was rejected upstream because it invites hosts to put retry *policy*
in a hook; the veto keeps the decision in the caller's own code, where the
streamed-output state already lives.

If your host also excluded stalls by hand, delete that: it is the default.
`RetryStalled: true` restores retrying them.

---

## 3. Stop synthesizing a profile per model

`Router.Chat` now honors a non-empty `request.Model`, which overrides the
profile's pinned model while the profile still supplies the provider, its
options, and its secrets:

```go
resp, err := r.Chat(ctx, "default", llm.Request{
    Model:    userChosenModel,          // overrides the profile's model
    Messages: msgs,
}, onEvent)
```

Before, a host that let users switch models at runtime had to build a
`config.ModelProfile` per model and call `Apply` on every switch — mutating
global router configuration to express a per-request choice, with all the
locking and staleness that implies. Delete that machinery for the chat path.

The same override applies to the embeddings path. It does **not** apply
everywhere; see below.

---

## What does not migrate

Two things invokr could not delete. Both are real limits of v0.7.0, not
oversights in the migration.

**`Capabilities` has no model override.** The signature is
`Capabilities(ctx, profileID)`, and it reports on `profile.Model` — the pinned
one. A host that switches models per request with `request.Model` and then asks
for capabilities or metadata (context window, tool support) gets the answer for
the *profile's* model, not the one it is about to call. So a host that needs
correct per-model metadata still has to keep a profile pinning each exact model,
and the derived-profile pattern survives there even though the chat path no
longer needs it. Budget for keeping both paths until the override reaches
`Capabilities`.

**`config.ModelProfile` still requires a non-empty `Model`.** Validation
rejects a profile with a provider and no model (`model profile %q requires
provider and model`). Combined with the override, the natural shape — a
"provider selection only" profile that always takes its model from the request —
is still not expressible. Hosts pin an arbitrary model in such a profile and
override it on every call, which works but means the config file states
something the host never uses, and a request that forgets the override silently
runs the placeholder.

---

## Smaller pickups

Not migration work, but cheap to adopt while you are in the file:

- **HTTP options** — `openaicompat.Config` takes `HTTPClient`, `HTTPTimeout`
  (whole request; default 10m) and `StallTimeout` (gap between stream reads,
  including the wait for the first chunk; default 5m, negative disables). A
  supplied `HTTPClient`'s own `Timeout` wins over `HTTPTimeout`. Hosts that
  wrapped the transport to get a timeout can drop the wrapper.
- **Env secrets** — `router.Open` defaults `Secrets` to `EnvResolver`, which
  resolves `env:` and `file:` references with the standard library alone. If
  you only need those, stop importing `router/secret` and shed `age`,
  `go-keyring`, and dbus.
- **Trust root and install orchestration** — see the corresponding sections of
  [`integration.md`](integration.md); neither changes code you already wrote.

---

## Checklist

- [ ] Root module at v0.7.0; submodules bumped when the proxy indexes their tags.
- [ ] Cause-rewording deleted; only host-specific operator wordings remain, each `%w`-wrapping the typed error.
- [ ] Hand-rolled backoff replaced by `RetryPolicy`, with `MaxAttempts = retries + 1`.
- [ ] Any "unsafe to re-issue" condition expressed as a non-unwrapping veto error, unwrapped after `Do`.
- [ ] Per-model profile synthesis deleted from the chat path — and knowingly kept for `Capabilities`.
