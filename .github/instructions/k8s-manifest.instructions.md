---
applyTo: "k8s/**/*.yml,k8s/**/*.yaml"
---

The `k8s/` directory contains raw Kubernetes manifests that should stay consistent with the Helm chart in `spectral-cloud/` and with the server's real runtime contract.

If you change image names, container ports, probes, environment variables, persistence mounts, labels, or scaling behavior here, check whether the same deployment assumptions also need to change in `spectral-cloud/`, `docker-compose.yml`, and the ops docs.

Prefer explicit, runnable manifests over placeholder-only examples. Keep health/readiness paths aligned with the actual HTTP endpoints exposed by `cmd/spectral-cloud`.

Do not let the raw manifest drift into a different product shape than the Helm deployment unless the repository is intentionally supporting both variants.
