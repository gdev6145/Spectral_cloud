---
applyTo: "cmd/spectral-agent/supervisor.sh"
---

`cmd/spectral-agent/supervisor.sh` is the local multi-agent demo/orchestration helper. Keep it aligned with the current `spectral-agent` flag contract, built-in capability names, and README usage examples.

If you change the launched agent set, capability lists, tags, polling cadence, or binary discovery behavior here, verify the same concepts still exist in `cmd/spectral-agent/main.go` and in the documented supervisor workflow.

Preserve the script's explicit failure behavior when neither a built binary nor `go` is available. This file is meant to help operators bootstrap a local pool, not to silently skip missing prerequisites.

Be careful with restart-loop behavior and process handling: this script should remain easy to stop cleanly and predictable to run on a developer workstation.
