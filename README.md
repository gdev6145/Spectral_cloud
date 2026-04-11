# Spectral_cloud

Spectral-Cloud is a decentralized, edge-optimized hosting environment for sovereign AI agents. It combines a UDP mesh overlay, a lightweight control plane, and observability tooling to run autonomous systems with low latency and minimal centralized dependencies.

**Highlights**
- Mesh networking with HMAC-signed heartbeats and route TTLs
- Secure HTTP API with fine-grained auth rules, admin paths, and rate limiting
- BoltDB persistence with migration, backups, compaction, and validation tooling
- Prometheus metrics, alerts, and Alertmanager wiring

**Quick Start**

```bash
cd /path/to/Spectral_cloud
go run ./cmd/spectral-cloud
```

Quick links:
- Architecture overview: `docs/spectral-cloud-architecture.md`
- API reference: `docs/api.md`
- Configuration reference: `docs/configuration.md`

**Standalone Agent**

```bash
cd /path/to/Spectral_cloud
go run ./cmd/spectral-agent -server http://localhost:8080 -api-key <tenant-or-api-key> -id edge-01 -capabilities echo,hash
```

Use a tenant-scoped key when the control plane is running in multi-tenant mode so the agent binds to the intended tenant.

To launch the bundled multi-agent demo supervisor, build `spectral-agent` first or run it in an environment with `go` on `PATH`, then set `SPECTRAL_API_KEY` when the server requires auth:

```bash
cd /path/to/Spectral_cloud
SPECTRAL_API_KEY=<tenant-or-api-key> ./cmd/spectral-agent/supervisor.sh http://localhost:8080 default
```

**Core Endpoints**
- `GET /` -> basic banner
- `GET /health` -> JSON health
- `GET /ready` -> readiness (DB + server ready)
- `GET /auth/whoami` -> resolved auth/tenant information for the presented credential
- `GET /metrics` -> Prometheus metrics
- `GET/PATCH/DELETE /agents?id=X` -> inspect, update, or deregister a tenant-scoped agent
- `POST /agents/register` -> register or refresh an agent record
- `GET/POST /agents/jobs` -> list jobs or submit work for the resolved tenant
- `GET/PATCH/DELETE /agents/jobs/{id}` -> inspect, update, or cancel a single tenant-scoped job
- `GET /agents/jobs/claim?agent_id=X&capability=Y` -> claim the next pending tenant job
- `GET/POST /schedules` -> list or create interval-based scheduled jobs
- `GET/PATCH/DELETE /schedules/{id}` -> inspect, update, pause/resume, or delete a schedule
- `GET/POST /agent-groups` -> list or create tenant-scoped agent groups
- `GET/PATCH/DELETE /agent-groups/{id}` -> fetch, rename, or delete a tenant-scoped agent group
- `GET /agent-groups/{id}/next` -> select the next available group member
- `POST /agent-groups/{id}/members` -> add a weighted member to a tenant-scoped group
- `PATCH/DELETE /agent-groups/{id}/members/{agentID}` -> update weight or remove a group member
- `POST /blockchain/add` -> add block (JSON array of transactions)
- `GET /routes` -> list routes
- `POST /routes?destination=X&latency=1&throughput=10&ttlSeconds=60` -> add a route (optional TTL)
- `GET/PATCH/DELETE /routes/{destination}` -> inspect, update, or delete a single tenant route
- `GET /routes/best?tag=region:us-west&tag=site:edge-a&max_latency=20&satellite_penalty=0` -> pick the best matching route for an edge/location filter
- `GET /routes/stats?tag=region:us-west` -> aggregate route metrics for a filtered edge subset, including per-region and per-site summaries
- `GET /routes/topology?tag=region:us-west&tag=site:edge-a` -> group a filtered edge subset by region/site and expose the best route per grouping
- `GET /routes/resolve?region=us-west&site=edge-a&tag=tier:premium&max_scope=region&explain=true&alternatives=2` -> resolve the nearest edge route with caller-limited fallback, optional diagnostics, and ranked backup routes
- `POST /routes/resolve/batch` -> resolve many edge route requests in one call with per-item status, diagnostics, and alternatives
- `GET /routes/filter?tag=region:us-west&tag=site:edge-a` -> filter routes by multiple tag dimensions
- `POST /proto/data` -> protobuf `DataMessage` request, returns protobuf `Ack`
- `POST /proto/control` -> protobuf `ControlMessage` request, returns protobuf `Ack`
- `GET/POST /admin/notifications` -> list or create tenant-scoped notification rules
- `GET/PATCH/DELETE /admin/notifications/{id}` -> inspect, update, enable/disable, or delete a tenant-scoped notification rule
- `GET /circuit` -> inspect circuit breaker state for agents
- `POST /circuit/reset?agent_id=X` -> manually reset a circuit breaker
- `GET /admin/status` -> backup/compaction status (admin-only)
- `GET /admin/mesh` -> mesh config/stats/anomaly state (admin-only)
- `GET /admin/tenants` -> list tenants and per-tenant counts (admin-only)

If `TENANT_KEYS`, `TENANT_WRITE_KEYS`, or `API_KEY` is set, include `Authorization: Bearer <key>` or `X-API-Key: <key>`.
Multi-tenant mode uses `TENANT_KEYS=tenant:key` mappings and isolates control-plane state such as blockchain data, routes, agents, jobs, groups, and notification rules by tenant.

**Docker Compose**

```bash
cd /root/Spectral_cloud
docker compose up --build
```

**Helm (Kubernetes)**

```bash
cd /root/Spectral_cloud
helm install spectral-cloud ./spectral-cloud
```

Update image settings in `spectral-cloud/values.yaml` if you publish a custom image.

**Published Artifacts**
- Docker image: `ghcr.io/gdev6145/spectral-cloud` (tags match releases)
- Helm chart repo: `https://gdev6145.github.io/Spectral_cloud` (chart `spectral-cloud`)

**Configuration**

Full configuration reference: `docs/configuration.md`.

Tenant quotas and per-tenant rate limiting can be enabled with `TENANT_MAX_BLOCKS`, `TENANT_MAX_ROUTES`, `TENANT_RATE_RPS`, and `TENANT_RATE_BURST`.

**Persistence Notes**
- BoltDB is the primary store at `DATA_DIR/spectral.db`.
- Legacy JSON files are migrated into BoltDB on startup.
- Jobs, schedules, agent groups, and notification rules reload from BoltDB on startup.
- Invalid blocks are ignored and the chain truncates at first invalid block.

**Maintenance**

```bash
cd /root/Spectral_cloud
# validate/repair
go run ./cmd/spectralctl validate --data-dir ./data
go run ./cmd/spectralctl repair --data-dir ./data

# backup/compaction
go run ./cmd/spectralctl backup --db-path ./data/spectral.db --out ./data/spectral.db.bak
go run ./cmd/spectralctl backup --db-path ./data/spectral.db --out ./data/spectral.db.bak --key <base64>
go run ./cmd/spectralctl compact --db-path ./data/spectral.db --out ./data/spectral.db.compacted
go run ./cmd/spectralctl compact --db-path ./data/spectral.db --in-place

# key utilities
go run ./cmd/spectralctl keygen
go run ./cmd/spectralctl rekey --in ./data/spectral.db.enc --out ./data/spectral.db.rekey --old-key <base64> --new-key <base64>

# gRPC mesh send
go run ./cmd/spectralctl mesh-send --addr localhost:9091 --api-key devkey --tenant default --kind data --payload "hello"
go run ./cmd/spectralctl mesh-send --addr localhost:9091 --api-key devkey --tenant default --kind control --control-type heartbeat

# gRPC mesh load (count/rate) and watch
go run ./cmd/spectralctl mesh-send --addr localhost:9091 --api-key devkey --tenant default --kind data --count 100 --rate 10
go run ./cmd/spectralctl mesh-watch --addr localhost:9091 --api-key devkey --tenant default --interval 5s
go run ./cmd/spectralctl mesh-load --addr localhost:9091 --api-key devkey --tenant default --kind data --duration 10s --concurrency 4 --rate 100
go run ./cmd/spectralctl mesh-load --addr localhost:9091 --api-key devkey --tenant default --kind data --count 1000 --csv ./mesh.csv --hist 1,5,10,25,50,100,250,500,1000
go run ./cmd/spectralctl mesh-load --addr localhost:9091 --api-key devkey --tenant default --kind data --duration 20s --ramp-start 2 --ramp-step 2 --ramp-interval 5s --ramp-max 8 --window 1s
go run ./cmd/spectralctl mesh-load --addr localhost:9091 --api-key devkey --tenant default --kind data --duration 10s --window 1s --window-live --window-live-json
go run ./cmd/spectralctl mesh-load --addr localhost:9091 --api-key devkey --tenant default --kind data --duration 10s --window 1s --window-live # includes top error reason
go run ./cmd/spectralctl mesh-send --addr localhost:9091 --api-key devkey --tenant default --kind data --tls --tls-ca ./ca.pem --tls-server-name spectral-cloud
```

**Observability**
- Prometheus alerts live in `prometheus/alerts.yml`.
- Alertmanager config templates live in `alertmanager/`.
- Docker Compose wires Prometheus + Alertmanager by default.

**Docs**
- Protocol specification: `docs/protocol.md`
- Architecture overview: `docs/spectral-cloud-architecture.md`
- API reference: `docs/api.md`
- Configuration reference: `docs/configuration.md`
- Mesh TLS setup: `docs/mesh-tls.md`
- Backup and restore runbook: `docs/ops/backup-restore.md`
- Systemd deployment: `docs/ops/systemd.md`
- Release process: `docs/ops/release.md`
- DevOps runbooks: `DevOps_Runbooks.md`

**Tests**

```bash
cd /root/Spectral_cloud
go test ./...
```

**Proto Generation**

```bash
cd /root/Spectral_cloud
./scripts/gen-proto.sh
```

**Contributing**
See `CONTRIBUTING.md`.

**Security**
See `SECURITY.md`.

**License**
See `LICENSE`.
