---
applyTo: "cmd/spectral-cloud/static/**/*"
---

The dashboard is an embedded static UI served directly by `cmd/spectral-cloud`; there is no separate frontend build pipeline. Keep changes self-contained and compatible with the server endpoints it calls.

When changing UI behavior, keep the API usage aligned with the current HTTP routes, JSON shapes, and event names exposed by `cmd/spectral-cloud`.

Avoid introducing assumptions about a bundler, package manager, or asset pipeline unless the repository is intentionally being restructured to support one.

Preserve the UI's role as an operator/admin surface for live state, not a second source of business logic. Server-side contracts remain authoritative.
