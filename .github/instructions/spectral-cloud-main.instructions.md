---
applyTo: "cmd/spectral-cloud/*.go"
---

Treat `cmd/spectral-cloud` as wiring code for the control plane, not as a place to duplicate business logic from `pkg/*`.

Preserve the tenant flow: auth resolves the tenant, tenant information is stored in request context, handlers typically call `getState(r)`, and reads or writes should remain tenant-scoped unless the route is explicitly admin-only.

When changing HTTP handlers, keep response shapes consistent with the existing server. Error responses should continue to go through `writeError` and remain JSON-shaped as `{\"error\":\"...\"}`.

Be careful with middleware ordering in `newHandler`. Auth, audit logging, rate limiting, metrics, CORS, access logging, request IDs, and panic recovery are intentionally layered; do not reorder them casually.

If you add or change endpoints here, update the matching docs in `README.md`, `docs/api.md`, and `docs/configuration.md` when relevant.
