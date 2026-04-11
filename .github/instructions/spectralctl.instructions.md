---
applyTo: "cmd/spectralctl/*.go,cmd/spectralctl/**/*.json,docs/api.md,docs/ops/*.md,README.md"
---

Treat `spectralctl` as the operator-facing contract for validation, repair, backup, restore, mesh probing, and API maintenance tasks. Avoid changing command names, flags, or output semantics casually.

If you add or change a `spectralctl` subcommand, update all three surfaces together when relevant:

- the `usage()` text in `cmd/spectralctl/main.go`
- the README command examples
- the matching operations or API docs under `docs/`

This binary mixes local data-plane operations (`validate`, `repair`, `backup`, `restore`, `compact`) with HTTP and mesh client flows. Keep those responsibilities clear instead of blending unrelated behavior into one command.

For storage-related commands, prefer delegating to `pkg/store` behavior instead of re-implementing persistence logic in the CLI.

For mesh and API commands, keep request flags, auth headers, and response assumptions aligned with the server implementation in `cmd/spectral-cloud`.
