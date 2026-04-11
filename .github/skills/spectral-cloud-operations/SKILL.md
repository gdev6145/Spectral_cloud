---
name: spectral-cloud-operations
description: Guidance for operating and debugging Spectral_cloud deployments. Use when investigating health, readiness, metrics, alerts, admin endpoints, backup and restore, Docker Compose, or Helm behavior.
---

# Spectral Cloud operations and debugging

Use this skill for incident response, deployment checks, and operational debugging.

## Primary operational references

- `README.md`
- `DevOps_Runbooks.md`
- `docs/ops/backup-restore.md`
- `docs/ops/systemd.md`
- `docs/ops/release.md`
- `prometheus/alerts.yml`
- `alertmanager/`
- `spectral-cloud/` for Helm deployment assets
- `docker-compose.yml` for local observability stack wiring

## Operational checklist

1. Check `GET /health` for service health and `GET /ready` for readiness.
2. Inspect `GET /metrics` when debugging traffic, error rates, or scrape failures.
3. Use admin endpoints when relevant:
   - `GET /admin/status`
   - `GET /admin/mesh`
   - `GET /admin/tenants`
4. Prefer the least disruptive recovery path first:
   - restart a single unhealthy instance
   - verify metrics and logs
   - roll back to the last known-good image only if the issue is broader
5. For persistence issues, use `spectralctl` commands for validate, backup, compact, and rekey flows rather than inventing ad hoc recovery steps.

## Deployment commands

- Local service: `go run ./cmd/spectral-cloud`
- Docker Compose: `docker compose up --build`
- Helm: `helm upgrade --install spectral-cloud ./spectral-cloud`

## Persistence commands

- `go run ./cmd/spectralctl validate --data-dir ./data`
- `go run ./cmd/spectralctl backup --db-path ./data/spectral.db --out ./data/spectral.db.bak`
- `go run ./cmd/spectralctl compact --db-path ./data/spectral.db --out ./data/spectral.db.compacted`

When behavior changes during an ops-related fix, update the corresponding runbook or operations docs in the same change.
