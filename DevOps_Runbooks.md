# DevOps Runbooks

## Introduction
This document serves as a comprehensive guide for DevOps teams to manage operations and maintain continuous delivery workflows.

## Runbook Sections
1. **Incident Management**
   - Verify service health: `GET /health` on each node.
   - Check metrics: `/metrics` for request rates and error spikes.
   - If a single node is unhealthy, restart that instance first.
   - If cluster-wide issues, roll back to the last known-good image.

2. **Monitoring and Alerts**
   - Prometheus should scrape `node1:8080` in Docker Compose or the service DNS name in Kubernetes.
   - Alert on:
     - HTTP 5xx rate > 1% for 5 minutes.
     - No scrape target available for 2+ intervals.
   - Validate alert accuracy by checking `/health` and logs.

3. **Backup and Recovery**
   - This service is currently in-memory only; data is lost on restart.
   - When persistence is added, run daily backups and verify restore monthly.

4. **Deployment**
   - Docker Compose: `docker compose up --build`
   - Kubernetes: `helm upgrade --install spectral-cloud ./spectral-cloud`
   - Verify readiness: Kubernetes readiness probe should pass before routing traffic.

5. **Rollback**
   - Kubernetes: `helm rollback spectral-cloud <REVISION>`
   - Docker: redeploy previous image tag.

6. **Performance Optimization**
   - Tune `MAX_BODY_BYTES` and request timeouts for workload.
   - Review routing table growth if TTL is disabled.

7. **Environment Management**
   - Dev: local `go run` or Docker Compose.
   - Test/Prod: Helm chart with explicit image tag and resource settings.

## Conclusion
Consistently update this runbook with new procedures and practices as the DevOps landscape evolves.
