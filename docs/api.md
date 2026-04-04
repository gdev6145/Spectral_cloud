# API Reference

This document provides a concise summary of the HTTP API surface. All endpoints are served by the Spectral-Cloud control plane.

## Authentication
- If `API_KEY` is set, requests must include `Authorization: Bearer <key>` or `X-API-Key: <key>` unless the path is in `PUBLIC_PATHS`.
- If `WRITE_API_KEY` is set, write endpoints require the write key.
- If `ADMIN_API_KEY` is set, admin endpoints require the admin key (and may be restricted to local/CIDR allowlists).

## Core Endpoints

`GET /`
Returns a basic banner.

`GET /health`
Returns JSON health summary.

Example response:
```json
{"status":"ok","timestamp":"2026-04-04T12:34:56Z","blocks":0,"routes":0}
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
Accepts a protobuf `DataMessage` and returns a protobuf ACK `DataMessage`.
- Content-Type: `application/x-protobuf`
- Response Content-Type: `application/x-protobuf`

`POST /proto/control`
Accepts a protobuf `ControlMessage` and returns a JSON summary.

Example response:
```json
{"status":"ok","control_type":"HEARTBEAT","node_id":1}
```

## Admin Endpoints

`GET /admin/status`
Returns backup/compaction status snapshot.

`GET /admin/mesh`
Returns mesh config, stats, and anomaly state.

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
