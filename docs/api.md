# API Reference

This document provides a concise summary of the HTTP API surface. All endpoints are served by the Spectral-Cloud control plane.

## Authentication
- If `API_KEY` is set, requests must include `Authorization: Bearer <key>` or `X-API-Key: <key>` unless the path is in `PUBLIC_PATHS`.
- If `WRITE_API_KEY` is set, write endpoints require the write key.
- If `ADMIN_API_KEY` is set, admin endpoints require the admin key (and may be restricted to local/CIDR allowlists).
- If `TENANT_KEYS` is set, the API key maps to a tenant and all state is isolated per tenant.
- If `TENANT_WRITE_KEYS` is set, write endpoints can use tenant-scoped write keys even when `TENANT_KEYS` is not configured.

## Core Endpoints

`GET /`
Returns a basic banner.

`GET /health`
Returns JSON health summary.

Example response:
```json
{"status":"ok","timestamp":"2026-04-04T12:34:56Z","blocks":0,"routes":0}
```

`GET /ready`
Returns readiness status (DB + server readiness).

Example response:
```json
{"status":"ready","timestamp":"2026-04-04T12:34:56Z"}
```

`GET /auth/whoami`
Returns the tenant and access level resolved from the presented credential. If auth is configured and no valid credential is provided, returns `401`.

Example response:
```json
{"authenticated":true,"access":"tenant-write","tenant":"tenant-a"}
```

`GET /metrics`
Prometheus metrics endpoint.

## Agents

`GET /agents`
Lists agents for the resolved tenant. Supports `?capability=X` to filter by advertised capability and `?id=X` to fetch a single agent.

`PATCH /agents?id=X`
Partially updates a tenant-scoped agent record. Supported JSON fields include `status`, `addr`, `capabilities`, and `tags`.

Example request:
```json
{"status":"degraded","addr":"10.0.0.2:9002","capabilities":["vision","ocr"],"tags":{"region":"us-east"}}
```

`DELETE /agents?id=X`
Removes a tenant-scoped agent registration.

`POST /agents/register`
Creates or refreshes an agent registration for the resolved tenant.

`POST /agents/heartbeat?id=X&ttl_seconds=300`
Refreshes `last_seen` and optionally extends the TTL for an existing agent.

`POST /agents/status?id=X&status=healthy`
Updates the self-reported status for an existing agent. Valid statuses are `healthy`, `degraded`, and `unknown`.

`GET /agents/route?capability=X&count=N`
Returns the best available tenant-scoped agents for a capability.

## Jobs

`GET /agents/jobs`
Lists jobs for the resolved tenant.

`POST /agents/jobs`
Submits a new job. Either `agent_id` or `capability` is required. When only `capability` is provided, the control plane auto-selects the best healthy tenant agent that advertises that capability.

Example request:
```json
{"capability":"ocr","payload":{"file":"doc.pdf"},"priority":5,"ttl_seconds":3600}
```

`GET /agents/jobs/claim?agent_id=X&capability=Y`
Claims the next pending job for the resolved tenant. At least one of `agent_id` or `capability` is required.

`GET /agents/jobs/{id}`
Fetches a single job if it belongs to the resolved tenant.

`PATCH /agents/jobs/{id}`
Updates a job status/result/error payload for the resolved tenant.

Example request:
```json
{"status":"done","result":"42"}
```

`DELETE /agents/jobs/{id}`
Cancels a job if it belongs to the resolved tenant and is not already terminal.

## Agent Groups

`GET /agent-groups`
Lists agent groups for the resolved tenant.

`POST /agent-groups`
Creates a new tenant-scoped agent group.

Example request:
```json
{"name":"workers"}
```

`GET /agent-groups/{id}`
Fetches a single agent group if it belongs to the resolved tenant.

`DELETE /agent-groups/{id}`
Deletes a tenant-scoped agent group.

`POST /agent-groups/{id}/members`
Adds an agent to a tenant-scoped group.

Example request:
```json
{"agent_id":"agent-1"}
```

`DELETE /agent-groups/{id}/members/{agentID}`
Removes an agent from the group.

`GET /agent-groups/{id}/next`
Returns the next available group member using round-robin selection and the circuit breaker allow check.

`POST /blockchain/add`
Append a block with a JSON array of transactions.

Example request:
```json
[{"sender":"a","recipient":"b","amount":1}]
```

`GET /routes`
Returns current route table as JSON.

`POST /routes?destination=X&latency=1&throughput=10&ttlSeconds=60`
Adds a route. `ttlSeconds` is optional.

`POST /proto/data`
Accepts a protobuf `DataMessage` and returns a protobuf `Ack`.
- Content-Type: `application/x-protobuf`
- Response Content-Type: `application/x-protobuf`

`POST /proto/control`
Accepts a protobuf `ControlMessage` and returns a protobuf `Ack`.
- Content-Type: `application/x-protobuf`
- Response Content-Type: `application/x-protobuf`

## Mesh gRPC

If `MESH_GRPC_ADDR` is set, the control plane exposes gRPC `MeshService` from `proto/mesh.proto`.
Use `spectralctl mesh-send` to send test messages.
Use `spectralctl mesh-watch` for periodic heartbeat acks.
Use `spectralctl mesh-load` for load testing with latency statistics.
`mesh-load` supports histogram buckets (`--hist`) and CSV export (`--csv`).
`mesh-load` supports per-window stats (`--window`) and concurrency ramping (`--ramp-*`).
`mesh-load` can stream window stats live with `--window-live` or JSON lines with `--window-live-json`.
Live window output includes the top error reason (if any) and count.
All mesh CLI commands support TLS flags: `--tls`, `--tls-ca`, `--tls-server-name`, and `--tls-insecure-skip-verify`.
TLS setup guide: `docs/mesh-tls.md`.

## Admin Endpoints

`GET /admin/status`
Returns backup/compaction status snapshot.

`GET /admin/notifications`
Lists notification rules for the resolved tenant.

`POST /admin/notifications`
Creates a new notification rule for the resolved tenant.

Example request:
```json
{"name":"agent-events","webhook_url":"https://example.com/hook","event_types":["agent.registered"]}
```

`DELETE /admin/notifications/{id}`
Deletes a notification rule if it belongs to the resolved tenant.

`GET /admin/mesh`
Returns mesh config, stats, and anomaly state.

`GET /admin/tenants`
Returns the list of tenants with per-tenant block/route counts.

## Circuit Breakers

`GET /circuit`
Lists all circuit breakers. Supports `?agent_id=X` to fetch a single breaker.

`POST /circuit/record`
Records a success or failure for an agent breaker.

Example request:
```json
{"agent_id":"agent-x","success":false}
```

`POST /circuit/reset?agent_id=X`
Manually resets a breaker and returns the updated breaker state.

## Examples

Send a `DataMessage` (Protobuf):
```bash
curl -s -X POST \
  -H 'Content-Type: application/x-protobuf' \
  --data-binary @data_message.bin \
  http://localhost:8080/proto/data
```

Send a `ControlMessage` (Protobuf):
```bash
curl -s -X POST \
  -H 'Content-Type: application/x-protobuf' \
  --data-binary @control_message.bin \
  http://localhost:8080/proto/control
```
