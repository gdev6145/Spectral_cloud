---
applyTo: "README.md,docs/**/*.md,DevOps_Runbooks.md"
---

This repository treats docs as part of the product surface, not as an afterthought. When behavior changes, update the specific doc that users or operators will actually consult instead of only adjusting a high-level summary.

Use the existing doc split consistently:

- `README.md` for entrypoints, quick-start flows, and high-value command examples
- `docs/api.md` for HTTP endpoint contracts and example payloads
- `docs/configuration.md` for environment variables and defaults
- `docs/protocol.md` and `docs/mesh-tls.md` for mesh/protobuf/TLS behavior
- `docs/ops/*.md` and `DevOps_Runbooks.md` for operational procedures

Prefer correcting the exact source of truth over duplicating new instructions in multiple places. If a command, endpoint, flag, environment variable, or deployment behavior changes, search for all user-facing references and keep them aligned in the same change.

Keep docs concrete and command-oriented. This repo already favors examples with real commands, flags, endpoints, and JSON payloads over abstract guidance.
