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

## Schedules

`GET /schedules`
Lists schedules for the resolved tenant.

`POST /schedules`
Creates a recurring interval-based schedule for the resolved tenant. `interval_seconds` must be greater than zero.

Example request:
```json
{"name":"hourly-sync","capability":"sync","agent_id":"worker-1","payload":{"mode":"full"},"interval_seconds":3600}
```

The schedule response includes the stored interval as `interval_ns`, the `active` flag, `last_run`, and `run_count`.

`GET /schedules/{id}`
Fetches a single schedule by ID if it belongs to the resolved tenant.

`PATCH /schedules/{id}`
Partially updates a schedule for the resolved tenant. Supported JSON fields are `name`, `agent_id`, `capability`, `payload`, `interval_seconds`, and `active`.

Example request:
```json
{"name":"paused-sync","interval_seconds":7200,"active":false}
```

`DELETE /schedules/{id}`
Deletes a schedule if it belongs to the resolved tenant.

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

`PATCH /agent-groups/{id}`
Partially updates a tenant-scoped agent group. Supported JSON fields: `name`.

Example request:
```json
{"name":"workers-east"}
```

`DELETE /agent-groups/{id}`
Deletes a tenant-scoped agent group.

`POST /agent-groups/{id}/members`
Adds an agent to a tenant-scoped group.

Example request:
```json
{"agent_id":"agent-1","weight":2}
```

`PATCH /agent-groups/{id}/members/{agentID}`
Updates a group member for the resolved tenant. Supported JSON fields: `weight` (must be greater than zero).

Example request:
```json
{"weight":3}
```

`DELETE /agent-groups/{id}/members/{agentID}`
Removes an agent from the group.

`GET /agent-groups/{id}/next`
Returns the next available group member using weighted round-robin selection and the circuit breaker allow check.

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

`GET /routes/{destination}`
Fetches a single route by destination for the resolved tenant.

`PATCH /routes/{destination}`
Partially updates a route for the resolved tenant. Supported JSON fields are `latency`, `throughput`, `ttl_seconds`, `satellite`, and `tags`.

Example request:
```json
{"latency":5,"throughput":250,"ttl_seconds":60,"satellite":true,"tags":{"region":"us"}}
```

`DELETE /routes/{destination}`
Deletes a single route by destination for the resolved tenant.

`GET /routes/best`
Returns the best route for the resolved tenant using the route scoring model.
Supports optional edge-oriented filters: `max_latency`, `min_throughput`, `satellite`, repeated `tag=key:value` parameters, and `satellite_penalty`.

Example request:
```text
/routes/best?tag=region:us-west&tag=site:edge-a&max_latency=20&satellite_penalty=0
```

`GET /routes/stats`
Returns aggregate route statistics for the resolved tenant, including edge-oriented `by_region` and `by_site` breakdowns derived from route tags. Supports the same optional route filters as `/routes/best` except `satellite_penalty`.

`GET /routes/topology`
Returns an edge topology view grouped by route `region` and `site` tags, including the best route for each region/site grouping. Supports the same optional route filters as `/routes/best` plus optional `satellite_penalty`.

`GET /routes/resolve`
Resolves the nearest edge route with fallback from exact `site` match to `region` match and finally an untagged global route. Supports `region`, `site`, and the same optional filters as `/routes/best`, including repeated `tag=key:value` parameters. Set `max_scope=site|region|global` to limit how far fallback may proceed. Set `explain=true` to include fallback attempts, candidate counts, and the selected scope in the response. Set `alternatives=N` to include up to `N` ranked backup routes from the selected scope.

Example request:
```text
/routes/resolve?region=us-west&site=edge-a&tag=tier:premium&tag=region:us-west&max_scope=region&explain=true&alternatives=2
```

`POST /routes/resolve/batch`
Resolves multiple edge route requests in one call. Each item can supply the same fields used by `GET /routes/resolve` (`region`, `site`, `max_scope`, `explain`, `alternatives`, `max_latency`, `min_throughput`, `satellite`, `tags`, `satellite_penalty`) plus an optional `id` echoed back in the result. The request body may also include `defaults` to apply shared resolver settings across all items. The endpoint always returns a batch result list; each item includes its own `status`, and the top-level response includes a `summary` of ok/not_found/invalid counts.

`GET /routes/filter`
Returns all routes matching the provided criteria. Supports `max_latency`, `min_throughput`, `satellite`, and repeated `tag=key:value` parameters that must all match.

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
{"name":"agent-events","webhook_url":"https://example.com/hook","event_types":["agent_registered"]}
```

`GET /admin/notifications/{id}`
Fetches a notification rule if it belongs to the resolved tenant.

`PATCH /admin/notifications/{id}`
Partially updates a notification rule for the resolved tenant. Supported JSON fields are `name`, `webhook_url`, `secret`, `event_types`, and `active`.

Example request:
```json
{"name":"agent-events-paused","active":false,"event_types":["agent_heartbeat"]}
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

## Message Queue

A lightweight, multi-tenant, in-memory topic-based message queue. Messages are tenant-scoped and delivered FIFO.

`GET /mq`
Lists all topics for the current tenant with pending message counts.

Response:
```json
{"topics": [{"topic": "ingest", "tenant": "default", "pending": 3}], "count": 1}
```

`POST /mq/{topic}`
Publishes a message to the named topic.

Request body:
```json
{"payload": {"key": "value"}}
```

Response: the published `Message` object with `id`, `topic`, `tenant`, `payload`, and `created_at`.

`GET /mq/{topic}?count=N`
Consumes (dequeues) up to `N` messages (default 1, 0 = all pending).

Response:
```json
{"messages": [{...}, ...], "count": 2}
```

`DELETE /mq/{topic}`
Purges all pending messages from the topic.

Response:
```json
{"purged": 3, "topic": "ingest"}
```
