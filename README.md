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
- `POST /proto/data` -> protobuf `DataMessage` request, returns `DataMessage` ACK
- `POST /proto/control` -> protobuf `ControlMessage` request, returns JSON summary
- `GET /admin/status` -> backup/compaction status (admin-only)
- `GET /admin/mesh` -> mesh config/stats/anomaly state (admin-only)

If `API_KEY` is set, include `Authorization: Bearer <key>` or `X-API-Key: <key>`.

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

**Configuration**

Full configuration reference: `docs/configuration.md`.

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
