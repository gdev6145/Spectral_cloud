# Spectral_cloud
"Spectral-Cloud is a decentralized, edge-optimized hosting environment designed for sovereign AI agents. By utilizing a mesh of local consumer hardware and high-speed satellite backhaul, Spectral-Cloud provides a low-latency, 'invisible' infrastructure layer that allows autonomous systems like Zenith to operate independently of centralized big-tech

## Quick Start (Local)

Run the minimal HTTP service:

```bash
cd /root/Spectral_cloud
go run ./cmd/spectral-cloud
```

Environment variables:
- `PORT` (default `8080`)
- `MAX_BODY_BYTES` (default `1048576`)
- `DATA_DIR` (default `./data`, or `/data` in containers)
- `DB_PATH` (default `${DATA_DIR}/spectral.db`)
- `BACKUP_RETENTION` (default `5`)
- `BACKUP_INTERVAL` (optional duration like `1h`; enables scheduled backups)
- `BACKUP_DIR` (default `${DATA_DIR}/backups`)
- `BACKUP_KEY_B64` (optional; 32-byte base64 key for encrypted backups)
- `API_KEY` (optional; when set, all endpoints except `/health` require auth)
- `ADMIN_ALLOW_REMOTE` (default `false`; if `true`, allows `/admin/status` from non-local hosts)
- `ADMIN_ALLOWLIST_CIDRS` (optional comma-separated CIDRs; when set, only these ranges can access `/admin/status`)
- `ADMIN_API_KEY` (optional; if set, required for admin paths)
- `ADMIN_WRITE_KEY` (optional; if set, required for admin write methods on admin paths)
- `WRITE_API_KEY` (optional; if set, required for non-admin write methods)
- `PUBLIC_PATHS` (optional comma-separated paths, supports `*` suffix for prefix match; can prefix with `METHOD:` or `METHOD ` e.g. `GET:/routes`; default `/health`)
- `ADMIN_PATHS` (optional comma-separated paths, supports `*` suffix; can prefix with `METHOD:` or `METHOD `; default `/admin/status`)
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
- `MESH_PEERS` (optional comma-separated `host:port`)
- `MESH_HEARTBEAT_INTERVAL` (default `5s`)
- `MESH_ROUTE_TTL` (default `30s`)
- `MESH_SHARED_KEY` (optional; if set, HMAC-signs control messages)
- `MESH_SHARED_KEYS` (optional comma-separated keys; first is used for signing, all are accepted for verification)
- `MESH_PEER_KEYS` (optional comma-separated `peer=key` entries to override shared keys)
- `MESH_ANOMALY_WINDOW` (default `5`)
- `MESH_REJECT_RATE_THRESHOLD` (default `0.3`)
- `MESH_REJECT_BURST_THRESHOLD` (default `20`)
- `MESH_ANOMALY_MIN_SAMPLES` (default `50`)

Endpoints:
- `GET /` -> basic banner
- `GET /health` -> JSON health
- `GET /metrics` -> Prometheus metrics
- `POST /blockchain/add` -> add block (JSON array of transactions)
- `GET /routes` -> list routes
- `POST /routes?destination=X&latency=1&throughput=10&ttlSeconds=60` -> add a route (optional TTL)
- `POST /proto/data` -> protobuf `DataMessage` request, returns `DataMessage` ACK
- `POST /proto/control` -> protobuf `ControlMessage` request, returns JSON summary

If `API_KEY` is set, include `Authorization: Bearer <key>` or `X-API-Key: <key>`.

## Docker Compose

```bash
cd /root/Spectral_cloud
docker compose up --build
```

This brings up 5 nodes on ports `8080-8084`, plus Prometheus on `9090`.

## Helm (Kubernetes)

```bash
cd /root/Spectral_cloud
helm install spectral-cloud ./spectral-cloud
```

Update image settings in `spectral-cloud/values.yaml` if you publish a custom image.

## Notes

- The service persists blockchain and routes in BoltDB at `DATA_DIR/spectral.db`.
- Legacy JSON files (`blockchain.json`, `routes.json`) are migrated into BoltDB on startup.
- On load, a timestamped `.bak` is created for each persistence file or DB.
- Invalid blocks are ignored and the chain truncates at the first invalid block.
- Expired routes are removed on startup.

## Maintenance

Validate/repair persistence offline:

```bash
cd /root/Spectral_cloud
go run ./cmd/spectralctl validate --data-dir ./data
go run ./cmd/spectralctl repair --data-dir ./data
```

Use `--db-path` to point at a non-default database location.

Schema files used by `spectralctl` live in `cmd/spectralctl/schemas/`.

Backup/compact the BoltDB:

```bash
cd /root/Spectral_cloud
go run ./cmd/spectralctl backup --db-path ./data/spectral.db --out ./data/spectral.db.bak
go run ./cmd/spectralctl backup --db-path ./data/spectral.db --out ./data/spectral.db.bak --key <base64>
go run ./cmd/spectralctl compact --db-path ./data/spectral.db --out ./data/spectral.db.compacted
go run ./cmd/spectralctl compact --db-path ./data/spectral.db --in-place
go run ./cmd/spectralctl keygen
go run ./cmd/spectralctl rekey --in ./data/spectral.db.enc --out ./data/spectral.db.rekey --old-key <base64> --new-key <base64>
```

Status endpoint:

`GET /admin/status` returns backup/compaction state. It always requires `ADMIN_API_KEY` if set (otherwise `API_KEY`). It is local-only unless `ADMIN_ALLOW_REMOTE=true`. If `ADMIN_ALLOWLIST_CIDRS` is set, only clients in those CIDRs can access it.

Mesh networking:

Set `MESH_ENABLE=true`, `MESH_UDP_BIND`, and `MESH_PEERS` to start UDP heartbeats and populate routes automatically.

Mesh admin:

`GET /admin/mesh` returns mesh config, stats, and anomaly state (admin‑only).

Prometheus alerts:

`prometheus/alerts.yml` includes mesh anomaly and reject-rate alerts. Docker Compose mounts it automatically.

Alertmanager:

`alertmanager/alertmanager.yml` is mounted by Docker Compose and wired into Prometheus.

To enable Slack/Email/PagerDuty/Opsgenie, copy and edit:

`alertmanager/alertmanager.full.yml` (replace placeholders), then update the compose volume to point at it.

Env‑driven Alertmanager:

- `alertmanager/alertmanager.tmpl.yml` + `alertmanager/render.sh` can render config from env vars.
- Docker Compose includes an `alertmanager-config` init container that runs envsubst and writes to a shared volume.
- `proto/mesh.proto` mirrors the protocol spec; generated Go code lives in `pkg/proto/mesh.pb.go`.

## Tests

```bash
cd /root/Spectral_cloud
go test ./...
```

## Proto Generation

```bash
cd /root/Spectral_cloud
./scripts/gen-proto.sh
```
