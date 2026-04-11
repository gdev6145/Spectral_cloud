---
applyTo: "pkg/agent/*.go,pkg/jobs/*.go,pkg/scheduler/*.go,pkg/agentgroup/*.go,cmd/spectral-cloud/*.go,cmd/spectral-agent/*.go,docs/api.md,README.md"
---

Preserve the relationship between agent registration, heartbeats, job submission, job claiming, scheduling, and agent groups. These are separate packages, but they form one runtime workflow.

Agents and jobs are tenant-scoped. Keep tenant IDs explicit in request payloads, registry keys, queue records, and scheduler submissions.

The in-memory agent registry is intentionally ephemeral, while jobs, schedules, groups, and notification rules can be restored from BoltDB. Do not assume all agent-related state has the same persistence model.

Keep ID and status conventions stable unless a coordinated migration is part of the same change:

- agent health statuses such as `healthy`, `degraded`, `unknown`
- job statuses such as `pending`, `running`, `done`, `failed`, `cancelled`
- generated IDs like `job-N`, `sched-N`, and `rule-N`

If you change agent/job payloads, claim semantics, or scheduler behavior, update both the server and the standalone agent/CLI surfaces together, along with the matching docs and examples.
