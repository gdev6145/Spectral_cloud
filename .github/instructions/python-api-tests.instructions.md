---
applyTo: "tests/**/*.py"
---

The Python tests under `tests/` are a lightweight external-client check of the HTTP API. Treat them as contract tests for documented responses, not as the primary place for business logic.

Keep request paths, authentication headers, JSON fields, and expected status codes aligned with `README.md`, `docs/api.md`, and the Go server handlers. If an API contract changes, update these tests in the same change.

Prefer small, explicit HTTP assertions over re-implementing large chunks of the server in Python. When mirroring logic from Go for sanity checks, keep the mirror narrow and clearly tied to a documented behavior.

Preserve the split between standalone tests and integration tests gated by environment variables such as `SPECTRAL_URL`.
