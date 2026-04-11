---
applyTo: "pkg/store/*.go,pkg/jobs/*.go,pkg/scheduler/*.go,pkg/agentgroup/*.go,pkg/notify/*.go,docs/configuration.md,docs/ops/*.md"
---

Preserve BoltDB as the system of record and keep tenant scoping explicit. Data that is tenant-owned should stay in tenant buckets or tenant-keyed KV records, not in ad hoc global structures.

Changes in `pkg/store` should remain compatible with startup restoration behavior in `cmd/spectral-cloud`: jobs, schedules, agent groups, and notification rules are loaded back from BoltDB on boot.

Keep persisted key formats and prefixes stable unless a migration path is part of the same change. Other packages rely on predictable KV prefixes such as `job_*` and `sched_*`.

Do not break the built-in `default` tenant assumptions. Empty tenant names are treated as errors in storage APIs, and deleting the default tenant is intentionally blocked.

If you change backup, compaction, validation, or restore behavior, update the related operator docs and `spectralctl` usage references in the same change.
