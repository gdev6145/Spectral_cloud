# Spectral_cloud

Spectral-Cloud is a decentralized, edge-optimized hosting environment for sovereign AI agents. It combines a UDP mesh overlay, a lightweight control plane, and observability tooling to run autonomous systems with low latency and minimal centralized dependencies.

**Highlights**
- Mesh networking with HMAC-signed heartbeats and route TTLs
- Secure HTTP API with fine-grained auth rules, admin paths, and rate limiting
- BoltDB persistence with migration, backups, compaction, and validation tooling
- Prometheus metrics, alerts, and Alertmanager wiring

**Quick Start**

```bash
cd /root/Spectral_cloud
go run ./cmd/spectral-cloud
```

Quick links:
- Architecture overview: `docs/spectral-cloud-architecture.md`
- API reference: `docs/api.md`
- Configuration reference: `docs/configuration.md`

**Core Endpoints**
- `GET /` -> basic banner
- `GET /health` -> JSON health
- `GET /metrics` -> Prometheus metrics
- `POST /blockchain/add` -> add block (JSON array of transactions)
- `GET /routes` -> list routes
- `POST /routes?destination=X&latency=1&throughput=10&ttlSeconds=60` -> add a route (optional TTL)
- `POST /proto/data` -> protobuf `DataMessage` request, returns protobuf `Ack`
- `POST /proto/control` -> protobuf `ControlMessage` request, returns protobuf `Ack`
- `GET /admin/status` -> backup/compaction status (admin-only)
- `GET /admin/mesh` -> mesh config/stats/anomaly state (admin-only)
- `GET /admin/tenants` -> list tenants and per-tenant counts (admin-only)

If `TENANT_KEYS` or `API_KEY` is set, include `Authorization: Bearer <key>` or `X-API-Key: <key>`.
Multi-tenant mode uses `TENANT_KEYS=tenant:key` mappings and isolates blockchain/routes by tenant.

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
