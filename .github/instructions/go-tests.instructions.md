---
applyTo: "**/*_test.go"
---

Follow the repository's existing Go testing style instead of introducing a separate test framework or heavy mocking layer.

For HTTP handler tests, prefer the existing pattern used in `cmd/spectral-cloud/main_test.go`:

- use `t.TempDir()` for isolated data
- open a real BoltDB store with `store.Open(...)`
- build handlers with the repo's helper setup
- exercise endpoints with `httptest.NewServer`

Keep tests package-local unless there is a strong reason to test only the exported API. Current tests rely heavily on package-level helpers and internal wiring.

Prefer focused tests around one behavior or endpoint at a time, using the real package types where practical. This codebase already has many small integration-style unit tests rather than broad mocked tests.

When persistence is involved, check the restart/load expectations too, not just the in-memory mutation path.
