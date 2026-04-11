---
applyTo: "pkg/events/*.go,pkg/notify/*.go,pkg/webhook/*.go,cmd/spectral-cloud/*.go,docs/api.md"
---

Treat the event broker and notification rules as one flow: server handlers publish structured tenant-aware events, the broker fans them out in-process, and notification rules dispatch webhooks from those events.

Keep event names and payload shapes stable unless the same change updates all consumers. Event types are referenced across the broker, notification manager, SSE/event endpoints, and operator-facing docs.

Tenant boundaries matter here too. Notification rules are tenant-owned, and event delivery should not leak cross-tenant events unless the call path is explicitly admin-scoped.

The broker is intentionally non-blocking for subscribers and retains bounded history. Avoid changes that introduce backpressure from slow subscribers into normal request handling.

If you change webhook signing, notification rule fields, or event type naming, update the corresponding HTTP/admin surfaces and docs in the same change.
