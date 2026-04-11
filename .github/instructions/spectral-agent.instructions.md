---
applyTo: "cmd/spectral-agent/*.go,cmd/spectral-agent/**/*.sh,README.md,docs/api.md"
---

Treat `cmd/spectral-agent` as a real client of the control plane, not as an isolated utility. Changes here should preserve compatibility with the server endpoints and JSON shapes used for agent registration, heartbeats, job claiming, and job updates.

Keep the configuration contract stable across flags, environment variables, and README examples. The agent currently documents and uses:

- `SPECTRAL_SERVER`
- `SPECTRAL_TENANT`
- `SPECTRAL_AGENT_ID`
- `SPECTRAL_API_KEY`
- `SPECTRAL_CAPABILITIES`
- `SPECTRAL_TAGS`

Preserve the default behavior where environment variables override flags and the agent auto-generates an ID from hostname if none is provided.

When changing agent request payloads or endpoint expectations, update the corresponding server handler behavior and docs in the same change. `cmd/spectral-agent` and `cmd/spectral-cloud` should evolve together.

Be careful with capability handling. This binary contains built-in execution behavior, so new capabilities or payload formats should be documented and tested rather than silently inferred.
