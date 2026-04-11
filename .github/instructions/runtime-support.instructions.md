---
applyTo: "pkg/kv/*.go,pkg/circuit/*.go,pkg/healthcheck/*.go,cmd/spectral-cloud/*.go,docs/api.md,docs/configuration.md"
---

These packages are small, but they participate in live runtime behavior inside `cmd/spectral-cloud`, so changes here should be treated as control-plane behavior changes rather than internal refactors.

Keep the intended persistence model clear:

- `pkg/kv` is in-memory and TTL-based
- `pkg/circuit` is in-memory and per-agent
- `pkg/healthcheck` is an active background checker that updates agent status and emits health-related events

Do not accidentally make these packages tenant-agnostic. KV entries, circuit state, and health check results are all consumed in tenant-aware request paths.

Health checks are expected to be lightweight and operationally safe: they should not introduce blocking behavior that can stall the server, and any status/event changes should stay aligned with the agent registry and event broker behavior.

If you change runtime status names, health event names, TTL semantics, or exposed admin/API output, update the corresponding handlers and docs in the same change.
