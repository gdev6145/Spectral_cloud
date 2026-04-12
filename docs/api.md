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

`PATCH /admin/tenants/{id}/plan`
Upgrade or downgrade a tenant's subscription plan (admin-only).

Example request:
```json
{"plan":"pro"}
```

`GET /admin/usage`
Returns today's usage report across all tenants. Use `?tenant=X` to scope to a single tenant.

## SaaS / Self-Service

`POST /auth/signup`
Register a new tenant and receive an initial API key. Returns `409 Conflict` if the tenant already exists.

Example request:
```json
{"tenant_id":"acme","name":"Acme Corp","email":"admin@acme.com","plan":"pro"}
```

Example response:
```json
{"tenant_id":"acme","api_key":"sk-…","plan":"pro","quota":{...}}
```

`GET /tenant/me`
Returns the authenticated tenant's profile, today's usage, and live agent count.

`PATCH /tenant/me`
Updates the authenticated tenant's `name` and/or `email`.

Example request:
```json
{"name":"Acme Corp Updated","email":"ops@acme.com"}
```

`GET /tenant/keys`
Lists all sub-keys for the authenticated tenant. The secret (`key`) field is always redacted.

`POST /tenant/keys`
Creates a new sub-key. The secret is shown once in the response.

Example request:
```json
{"name":"ci-pipeline"}
```

`DELETE /tenant/keys/{id}`
Revokes a sub-key by its ID.

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

## AI Endpoints

All AI endpoints require `ANTHROPIC_API_KEY` to be set in the server environment. Requests that arrive when the key is absent receive `503 Service Unavailable`. AI state is tenant-scoped: chat sessions and prompt templates are isolated per tenant.

`POST /ai/infer`
Run a single Claude inference call. Supports three modes via the `mode` query parameter:
- `mode=direct` (default) — synchronous; returns `{"content":"...","model":"...","input_tokens":N,"output_tokens":N}`.
- `mode=async` — submits an inference job to the queue; returns the job record immediately.
- `stream=true` (with `mode=direct`) — proxies Claude's SSE stream to the caller as `text/event-stream`.

Example request:
```json
{"prompt":"Summarise this paragraph…","system":"You are a concise summariser.","model":"claude-3-5-haiku-latest","max_tokens":512}
```

`GET /ai/agents`
Lists agents that advertise the `inference` capability for the resolved tenant.

`GET /ai/sessions`
Lists active chat session IDs for the resolved tenant along with turn counts and last-updated timestamps.

Example response:
```json
{"count":2,"sessions":[{"session_id":"session-abc","turns":3,"updated_at":"2026-04-12T00:10:00Z"}]}
```

`POST /ai/chat/{session_id}`
Send a user message to a persistent multi-turn chat session. The session is created automatically on first use.

Example request:
```json
{"message":"What is the current cluster health?"}
```

`GET /ai/chat/{session_id}`
Retrieve the full message history for a chat session.

`DELETE /ai/chat/{session_id}`
Delete a chat session and its history.

`POST /ai/analyze`
Ask Claude to analyze live cluster health and return a concise, actionable summary. Accepts an optional `question` field to focus the analysis.

Example request:
```json
{"question":"Are there any degraded agents or routing concerns?"}
```

`POST /ai/route`
Describe a task in natural language. Claude picks the best matching agent capability from the live agent registry and submits a job automatically.

Example request:
```json
{"task":"Fetch the latest satellite telemetry and write a summary","payload":{"region":"us-west"}}
```

Example response:
```json
{"chosen_capability":"telemetry","agent_id":"agent-07","job":{...},"routing_model":"claude-3-5-haiku-latest"}
```

`POST /ai/extract`
Extract structured data from freeform text using a plain-English schema description.

Example request:
```json
{"text":"John Smith joined on 2024-01-15 as a senior engineer.","schema":"name (string), start_date (ISO date), role (string)"}
```

Example response:
```json
{"extracted":{"name":"John Smith","start_date":"2024-01-15","role":"senior engineer"},"model":"..."}
```

`POST /ai/judge`
Score text against evaluation criteria on a 0–10 scale.

Example request:
```json
{"text":"The agent responded within 50ms on every request.","criteria":"Responsiveness and reliability"}
```

Example response:
```json
{"score":9,"reasoning":"The agent demonstrated consistently low latency.","model":"..."}
```

`POST /ai/rerank`
Rank a list of candidate strings by relevance to a query. Returns candidates ordered from most to least relevant with per-item scores.

Example request:
```json
{"query":"lowest latency edge route for video","candidates":["route-a (50ms)","route-b (120ms)","route-c (30ms)"]}
```

`POST /ai/diff`
Explain the differences between two text versions in plain English.

Example request:
```json
{"before":"v1 config text","after":"v2 config text","context":"Nginx upstream block"}
```

`GET /ai/models`
Returns the list of Claude models available for use in AI endpoints.

`POST /ai/classify`
Assign one or more labels from a fixed set to text. Set `multi_label: true` to allow multiple labels.

Example request:
```json
{"text":"Agent is not responding to heartbeats.","labels":["info","warning","critical"],"multi_label":false}
```

`POST /ai/translate`
Translate text to a target language. `source_language` defaults to automatic detection.

Example request:
```json
{"text":"Hello, world!","target_language":"French","source_language":"English"}
```

`GET/POST /ai/templates`
List or create named prompt templates. Templates support `{{variable}}` placeholders.

`POST` example request:
```json
{"name":"summarise","system":"You are a concise summariser.","prompt":"Summarise the following in {{style}} style:\n{{text}}","max_tokens":256}
```

`GET /ai/templates/{name}`
Fetch a single prompt template by name.

`DELETE /ai/templates/{name}`
Delete a prompt template.

`POST /ai/templates/{name}/run`
Render a template's `{{variable}}` placeholders with supplied values and run the inference.

Example request body (key/value map of variable substitutions):
```json
{"style":"bullet points","text":"The deployment completed successfully after 3 retries."}
```

`POST /ai/loop`
Run a goal-directed agentic loop. Claude iterates up to `max_iterations` times, calling built-in tools (`list_agents`, `submit_job`, `get_job`, `list_routes`) until the goal is met or the iteration limit is reached.

Example request:
```json
{"goal":"Submit a diagnostic job to every degraded agent","max_iterations":10}
```

Example response:
```json
{"outcome":"completed","iterations":3,"steps":[{"action":"list_agents","result":"..."},...],"model":"..."}
```

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
