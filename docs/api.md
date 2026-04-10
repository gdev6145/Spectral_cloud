# API Reference

This document provides a concise summary of the HTTP API surface. All endpoints are served by the Spectral-Cloud control plane.

## Authentication
- If `API_KEY` is set, requests must include `Authorization: Bearer <key>` or `X-API-Key: <key>` unless the path is in `PUBLIC_PATHS`.
- If `WRITE_API_KEY` is set, write endpoints require the write key.
- If `ADMIN_API_KEY` is set, admin endpoints require the admin key (and may be restricted to local/CIDR allowlists).
- If `TENANT_KEYS` is set, the API key maps to a tenant and all state is isolated per tenant.

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

`GET /metrics`
Prometheus metrics endpoint.

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

`GET /admin/mesh`
Returns mesh config, stats, and anomaly state.

`GET /admin/tenants`
Returns the list of tenants with per-tenant block/route counts.

## Message Queue

`GET /mq`
List all topics with pending message counts for the authenticated tenant.

Example response:
```json
{"topics":[{"topic":"jobs","tenant":"default","pending":3}],"count":1}
```

`POST /mq/{topic}`
Publish a message to `{topic}`. Body: `{"payload":{...}}`.

Example:
```bash
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"payload":{"task":"summarise","doc":"report.pdf"}}' \
  http://localhost:8080/mq/jobs
```

Example response:
```json
{"id":"msg-1","topic":"jobs","tenant":"default","payload":{"doc":"report.pdf","task":"summarise"},"created_at":"2026-04-10T08:00:00Z"}
```

`GET /mq/{topic}?count=N`
Consume (dequeue) up to `count` messages from `{topic}` (default 1).

Example:
```bash
curl -s http://localhost:8080/mq/jobs?count=5
```

`DELETE /mq/{topic}`
Purge all pending messages from `{topic}`. Returns `{"purged":N,"topic":"jobs"}`.

## Notification Rules

`GET /admin/notifications`
List all notification rules.

`POST /admin/notifications`
Create a notification rule. Body: `{"name":"...","webhook_url":"...","secret":"...","event_types":["block_added"]}`.

`PATCH /admin/notifications/{id}`
Enable or disable a rule. Body: `{"active": true|false}`.

`DELETE /admin/notifications/{id}`
Delete a notification rule.

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
