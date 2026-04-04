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

Environment variables:
- `PORT` (default `8080`)
- `MAX_BODY_BYTES` (default `1048576`)
- `DATA_DIR` (default `./data`, or `/data` in containers)
- `DB_PATH` (default `${DATA_DIR}/spectral.db`)
- `BACKUP_RETENTION` (default `5`)
- `BACKUP_INTERVAL` (optional duration like `1h`; enables scheduled backups)
- `BACKUP_DIR` (default `${DATA_DIR}/backups`)
- `BACKUP_KEY_B64` (optional; 32-byte base64 key for encrypted backups)
- `API_KEY` (optional; when set, all endpoints except `PUBLIC_PATHS` require auth)
- `WRITE_API_KEY` (optional; if set, required for non-admin write methods)
- `ADMIN_API_KEY` (optional; if set, required for admin paths)
- `ADMIN_WRITE_KEY` (optional; if set, required for admin write methods on admin paths)
- `PUBLIC_PATHS` (comma-separated paths, supports `*` suffix; can prefix `METHOD:` or `METHOD `; default `/health`)
- `ADMIN_PATHS` (comma-separated paths, supports `*` suffix; can prefix `METHOD:` or `METHOD `; default `/admin/status`)
- `ADMIN_ALLOW_REMOTE` (default `false`; if `true`, allows admin paths from non-local hosts)
- `ADMIN_ALLOWLIST_CIDRS` (comma-separated CIDRs; only these ranges can access admin paths)
- `RATE_LIMIT_RPS` (default `10`)
- `RATE_LIMIT_BURST` (default `20`)
- `TLS_CERT_FILE`, `TLS_KEY_FILE` (optional; enables HTTPS)
- `COMPACT_ON_START` (default `false`)
- `COMPACT_INTERVAL` (optional duration like `6h`; enables scheduled compaction)
- `COMPACT_DIR` (default `${DATA_DIR}/compactions`)
- `COMPACT_RETENTION` (default `3`)
- `MESH_ENABLE` (default `false`)
- `MESH_UDP_BIND` (default `0.0.0.0:7000`)
- `MESH_NODE_ID` (optional; random if unset)
- `MESH_PEERS` (comma-separated `host:port`)
- `MESH_HEARTBEAT_INTERVAL` (default `5s`)
- `MESH_ROUTE_TTL` (default `30s`)
- `MESH_SHARED_KEY` (optional; HMAC-signs control messages)
- `MESH_SHARED_KEYS` (comma-separated keys; first signs, all verify)
- `MESH_PEER_KEYS` (comma-separated `peer=key` entries to override shared keys)
- `MESH_ANOMALY_WINDOW` (default `5`)
- `MESH_REJECT_RATE_THRESHOLD` (default `0.3`)
- `MESH_REJECT_BURST_THRESHOLD` (default `20`)
- `MESH_ANOMALY_MIN_SAMPLES` (default `50`)

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
