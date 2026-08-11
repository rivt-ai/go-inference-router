# ADR 0007 — Rename the repository and module to go-inference-router

Status: accepted · Date: 2026-08-11 · Supersedes the repository and module naming in ADR 0004

## Context

The repository, Go module, and Router binary carried the `invokr-llm` /
`invokr-model-router` names from ADR 0004. Those names tied a provider-neutral,
reusable runtime to the `Invokr` product that happens to be its first
consumer, which understates what the module is and overstates its coupling to
one host application. `Invokr` itself is unaffected by this decision: it
remains the host application that selects Model Profiles and launches the
Router as described in ADR 0004.

A naming review considered `go-model-gateway`, `go-llm-gateway`,
`go-ai-gateway`, `go-genai-gateway`, `go-inference-gateway`, and
`go-provider-gateway`, among others. `go-llm-gateway` and `go-ai-gateway`
collide with existing, unrelated Go projects. `gateway` also undersells the
mechanism: the Router resolves a configured Model Profile and dispatches to a
Provider Process, which is routing, not edge ingress/egress. `router` matches
the vocabulary already established for the `Router` concept in `CONTEXT.md`
and used throughout the codebase.

## Decision

Rename the repository and module to `go-inference-router`. This is a purely
mechanical rename with no protocol or behavior change:

- Module: `github.com/antonikliment/invokr-llm` becomes
  `github.com/rivt-ai/go-inference-router` (submodule paths follow the
  same substitution).
- Router binary: `invokr-model-router` becomes `go-inference-router`.
- Provider Process binaries: `invokr-provider-*` becomes
  `go-inference-router-provider-*`.
- OS config directory, keychain service name, and provider cache directory:
  `invokr` becomes `go-inference-router`.
- Workspace override directory: `.invokr/` becomes `.go-inference-router/`.
- Release/build-time environment variables: the `INVOKR_` prefix becomes
  `INFROUTER_` (for example, `INVOKR_REGISTRY_PUBLIC_KEY` becomes
  `INFROUTER_REGISTRY_PUBLIC_KEY`).

References to `Invokr` the product — the host application that consumes this
runtime — are unchanged.

## Consequences

- Existing `.invokr/models.yaml` workspaces and OS config files must move to
  `.go-inference-router/` and the new OS config directory; nothing reads the
  old paths.
- Any deployment invoking `invokr-model-router` or `invokr-provider-*`
  directly, or setting `INVOKR_*` environment variables, breaks until updated
  to the new binary and variable names.
- `go get github.com/antonikliment/invokr-llm` breaks once the GitHub
  repository itself is renamed to match this module path; the repository
  rename is a prerequisite this ADR does not itself perform.
- The Go rename is intentionally breaking, consistent with ADR 0004; the
  additive `llm.v1` process protocol is unaffected and remains the
  compatibility commitment.
