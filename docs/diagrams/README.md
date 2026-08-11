# Diagrams

Graphviz DOT sources for the architecture documentation. They are kept as text
so they diff cleanly; render them when you need pictures.

**GitHub does not render DOT in Markdown** — it supports Mermaid, not Graphviz.
So [`../architecture.md`](../architecture.md) carries simplified Mermaid
versions of the component graph and the provider state machine, which render
inline, and links here for the full detail. When you change one of those two
diagrams, update both.

```sh
dot -Tsvg components.dot        -o components.svg
dot -Tsvg request-lifecycle.dot -o request-lifecycle.svg
dot -Tsvg provider-lifecycle.dot -o provider-lifecycle.svg
dot -Tsvg install-lifecycle.dot -o install-lifecycle.svg
```

| File | What it shows |
|---|---|
| `components.dot` | Module and package structure, and the three ways a host consumes the project |
| `request-lifecycle.dot` | A streaming chat call from host to provider API and back, including failure branches |
| `provider-lifecycle.dot` | States of one Provider Definition inside a running Router |
| `install-lifecycle.dot` | Approval-gated install, launch-time re-verification, and removal |

Rendered output is not committed. Regenerate after editing a `.dot` file.
