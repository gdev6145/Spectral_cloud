---
applyTo: "docker-compose.yml,Dockerfile,spectral-cloud/**,prometheus/**,alertmanager/**,docs/ops/*.md,DevOps_Runbooks.md"
---

Keep local and deployment surfaces aligned. Changes to environment variables, ports, health checks, image behavior, or chart values should be reflected across the corresponding docs and deployment assets in the same change.

`docker-compose.yml` is not just a toy example here: it is the local observability and multi-node reference stack. Preserve the relationship between the service ports, the `/health` endpoint, and the Prometheus/Alertmanager services unless the change intentionally updates that workflow end-to-end.

When changing container startup behavior, keep it consistent with the main service defaults from `cmd/spectral-cloud` and `docs/configuration.md`, especially `PORT`, `DATA_DIR`, auth path settings, and mesh-related environment variables.

If you change Helm chart behavior under `spectral-cloud/`, keep image/tag expectations and values naming aligned with the release workflow in `.github/workflows/release.yml`.

Operational docs in `docs/ops/*.md` and `DevOps_Runbooks.md` should stay practical and command-oriented. Prefer updating existing runbooks over introducing parallel instructions elsewhere.
