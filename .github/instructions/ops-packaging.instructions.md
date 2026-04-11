---
applyTo: "docs/ops/**/*.md,docs/ops/*.service,docs/ops/*.logrotate"
---

Files under `docs/ops/` are operational deployment assets, not prose alone. Keep the Markdown guides and the shipped service/logrotate templates consistent with each other.

If you change systemd environment names, binary paths, working directories, or logging behavior, update both the documentation and the concrete asset files in the same change.

Operational docs in this repository should reflect the actual server/runtime contract from `cmd/spectral-cloud` and `cmd/spectralctl`, especially around persistence, backups, ports, and readiness endpoints.

Prefer small, explicit examples operators can copy with minimal editing.
