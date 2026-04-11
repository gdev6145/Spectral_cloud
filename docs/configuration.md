# Configuration

This document lists the environment variables used by Spectral-Cloud and their defaults. Values are read from the process environment at startup.

## HTTP Server
- `PORT` (default `8080`)
- `MAX_BODY_BYTES` (default `1048576`)
- `TLS_CERT_FILE` (optional; enables HTTPS when set)
- `TLS_KEY_FILE` (optional; enables HTTPS when set)

## Auth and Access Control
- `API_KEY` (optional; when set, all endpoints except `PUBLIC_PATHS` require auth)
- `WRITE_API_KEY` (optional; if set, required for non-admin write methods)
- `ADMIN_API_KEY` (optional; if set, required for admin paths)
- `ADMIN_WRITE_KEY` (optional; if set, required for admin write methods on admin paths)
- `TENANT_KEYS` (optional; `tenant:key` pairs for multi-tenant auth)
- `TENANT_WRITE_KEYS` (optional; `tenant:key` pairs for write-only keys; can be used without `TENANT_KEYS` for write-only tenant auth)
- `DEFAULT_TENANT` (default `default`; used when API_KEY/WRITE_API_KEY is used)
- `PUBLIC_PATHS` (comma-separated paths, supports `*` suffix; can prefix `METHOD:` or `METHOD `; default `/health`)
- `ADMIN_PATHS` (comma-separated paths, supports `*` suffix; can prefix `METHOD:` or `METHOD `; default `/admin/status`)
- `ADMIN_ALLOW_REMOTE` (default `false`; if `true`, allows admin paths from non-local hosts)
- `ADMIN_ALLOWLIST_CIDRS` (comma-separated CIDRs; only these ranges can access admin paths)

## Rate Limiting
- `RATE_LIMIT_RPS` (default `10`)
- `RATE_LIMIT_BURST` (default `20`)
- `TENANT_RATE_RPS` (default `0`; per-tenant RPS limit when > 0)
- `TENANT_RATE_BURST` (default `0`; per-tenant burst when > 0)

## Tenant Quotas
- `TENANT_MAX_BLOCKS` (default `0`; max blocks per tenant when > 0)
- `TENANT_MAX_ROUTES` (default `0`; max routes per tenant when > 0)

## Storage
- `DATA_DIR` (default `./data`, or `/data` in containers)
- `DB_PATH` (default `${DATA_DIR}/spectral.db`)
- BoltDB stores tenant-scoped blockchain data, routes, jobs, schedules, agent groups, and notification rules; persisted jobs/schedules/groups/notification rules are restored on startup.

## Backups
- `BACKUP_RETENTION` (default `5`)
- `BACKUP_INTERVAL` (optional duration like `1h`; enables scheduled backups)
- `BACKUP_DIR` (default `${DATA_DIR}/backups`)
- `BACKUP_KEY_B64` (optional; 32-byte base64 key for encrypted backups)

## Compaction
- `COMPACT_ON_START` (default `false`)
- `COMPACT_INTERVAL` (optional duration like `6h`; enables scheduled compaction)
- `COMPACT_DIR` (default `${DATA_DIR}/compactions`)
- `COMPACT_RETENTION` (default `3`)

## Mesh (UDP Overlay)
- `MESH_ENABLE` (default `false`)
- `MESH_UDP_BIND` (default `0.0.0.0:7000`)
- `MESH_NODE_ID` (optional; random if unset)
- `MESH_PEERS` (comma-separated `host:port`)
- `MESH_HEARTBEAT_INTERVAL` (default `5s`)
- `MESH_ROUTE_TTL` (default `30s`)
- `MESH_SHARED_KEY` (optional; HMAC-signs control messages)
- `MESH_SHARED_KEYS` (comma-separated keys; first signs, all verify)
- `MESH_PEER_KEYS` (comma-separated `peer=key` entries to override shared keys)
- `MESH_TENANT` (default `DEFAULT_TENANT`; tenant used for mesh routing updates)
- `MESH_GRPC_ADDR` (optional; enables gRPC MeshService on the given address)
- `MESH_GRPC_TLS_CERT` / `MESH_GRPC_TLS_KEY` (optional; enable TLS for gRPC)
- `MESH_GRPC_TLS_CLIENT_CA` (optional; when set, require client certs signed by this CA)
- `MESH_ANOMALY_WINDOW` (default `5`)
- `MESH_REJECT_RATE_THRESHOLD` (default `0.3`)
- `MESH_REJECT_BURST_THRESHOLD` (default `20`)
- `MESH_ANOMALY_MIN_SAMPLES` (default `50`)
