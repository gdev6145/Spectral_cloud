# Spectral Vault

**Spectral Vault** is a standalone, AI-managed cloud storage and catalog service that runs alongside [Spectral Cloud](../../README.md). It provides object/file storage with automatic organization, metadata cataloging, deduplication, and integrates with Spectral Cloud's message queue for asynchronous processing.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                        Spectral Vault                            │
│                                                                  │
│  ┌──────────────┐   upload    ┌──────────────┐                  │
│  │  vault-api   │────────────▶│  local FS    │                  │
│  │  (HTTP API)  │             │  (VAULT_DATA │                  │
│  └──────┬───────┘             │   _DIR)      │                  │
│         │ MQ publish          └──────────────┘                  │
│         ▼                                                        │
│  ┌──────────────┐             ┌──────────────┐                  │
│  │  Spectral    │◀── consume──│ vault-worker │                  │
│  │  Cloud MQ    │             │  (organizer) │                  │
│  │  /mq/ingest  │─── publish─▶│              │                  │
│  └──────────────┘             └──────┬───────┘                  │
│                                      │ db.Update                │
│                                      ▼                           │
│                               ┌──────────────┐                  │
│                               │  SQLite DB   │                  │
│                               │  (catalog)   │                  │
│                               └──────────────┘                  │
└──────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Binary | Role |
|-----------|--------|------|
| `vault-api` | `cmd/vault-api` | HTTP API for upload, download, metadata, and search |
| `vault-worker` | `cmd/vault-worker` | Background worker: consumes MQ messages, runs organizer, updates catalog |

### Internal packages

| Package | Path | Purpose |
|---------|------|---------|
| `storage` | `internal/storage` | Local filesystem object store (`{DataDir}/{tenant}/{id}/{filename}`) |
| `db` | `internal/db` | SQLite catalog — file records, folder index, full-text-style search |
| `organizer` | `internal/organizer` | Heuristic auto-naming, folder suggestion, tag extraction, and pluggable LLM hook |
| `spectralclient` | `internal/spectralclient` | HTTP client for Spectral Cloud MQ (`POST /mq/{topic}`, `GET /mq/{topic}`) |

---

## Quick Start

### Using Docker Compose

From the repository root:

```bash
docker compose up --build vault-api vault-worker
```

`vault-api` will be available at `http://localhost:8090`.

> **Note:** The compose setup connects vault-api and vault-worker to `node1` (Spectral Cloud). Set `SPECTRAL_API_KEY` in `docker-compose.yml` if your node requires authentication.

### Building locally

```bash
cd apps/spectral-vault
go build ./cmd/vault-api
go build ./cmd/vault-worker
```

### Running vault-api

```bash
export VAULT_DATA_DIR=/tmp/vault-data
export VAULT_ADDR=:8090
export SPECTRAL_URL=http://localhost:8080
export SPECTRAL_API_KEY=your-key-here
./vault-api
```

### Running vault-worker

```bash
export VAULT_DATA_DIR=/tmp/vault-data
export SPECTRAL_URL=http://localhost:8080
export SPECTRAL_API_KEY=your-key-here
export SPECTRAL_TENANT=default
./vault-worker
```

---

## Configuration

### vault-api

| Variable | Default | Description |
|----------|---------|-------------|
| `VAULT_DATA_DIR` | `./vault-data` | Directory for files and SQLite database |
| `VAULT_ADDR` | `:8090` | Listen address |
| `VAULT_TENANT_HEADER` | `""` | If set (e.g. `X-Tenant-ID`), reads tenant from this request header |
| `SPECTRAL_URL` | `""` | Spectral Cloud base URL for MQ integration |
| `SPECTRAL_API_KEY` | `""` | API key for Spectral Cloud auth |
| `SPECTRAL_MQ_INGEST_TOPIC` | `ingest` | MQ topic for ingest notifications |

### vault-worker

| Variable | Default | Description |
|----------|---------|-------------|
| `VAULT_DATA_DIR` | `./vault-data` | Directory for files and SQLite database (must match vault-api) |
| `SPECTRAL_URL` | *(required)* | Spectral Cloud base URL |
| `SPECTRAL_API_KEY` | `""` | API key for Spectral Cloud auth |
| `SPECTRAL_TENANT` | `default` | Tenant to consume MQ messages from |
| `SPECTRAL_MQ_INGEST_TOPIC` | `ingest` | Topic to consume |
| `SPECTRAL_MQ_RESULTS_TOPIC` | `ingest.results` | Topic to publish results to |
| `WORKER_POLL_INTERVAL` | `5s` | How often to poll for new messages |

---

## API Reference

### `POST /v1/files` — Upload a file

Multipart form upload.

**Form fields:**
- `file` (required) — the file to upload
- `folder` (optional) — override the AI-suggested folder path

**Headers:**
- `X-Tenant-ID: {tenant}` — if `VAULT_TENANT_HEADER=X-Tenant-ID` is configured

**Response `201 Created`:**
```json
{
  "id": "vf-3a9f2c1d",
  "tenant": "default",
  "original_name": "invoice_scan.pdf",
  "canonical_name": "2026-04-13_invoice_scan.pdf",
  "folder_path": "documents/invoices",
  "storage_key": "default/vf-3a9f2c1d/invoice_scan.pdf",
  "mime_type": "application/pdf",
  "size": 45231,
  "sha256": "e3b0c44298fc1c149afb...",
  "tags": "document,invoice",
  "summary": "PDF document uploaded on 2026-04-13",
  "created_at": "2026-04-13T07:00:00Z",
  "updated_at": "2026-04-13T07:00:00Z"
}
```

**Response `409 Conflict`** — duplicate file (same SHA-256 already stored for this tenant):
```json
{
  "error": "duplicate file",
  "existing_id": "vf-1b2c3d4e"
}
```

---

### `GET /v1/files/{id}` — Get file metadata

```bash
curl http://localhost:8090/v1/files/vf-3a9f2c1d
```

**Response `200 OK`:**
```json
{
  "id": "vf-3a9f2c1d",
  "tenant": "default",
  "original_name": "invoice_scan.pdf",
  "canonical_name": "2026-04-13_invoice_scan.pdf",
  "folder_path": "documents/invoices",
  ...
}
```

---

### `GET /v1/files/{id}/download` — Download a file

```bash
curl -o invoice_scan.pdf http://localhost:8090/v1/files/vf-3a9f2c1d/download
```

Streams the file bytes. Sets `Content-Disposition: attachment; filename="<canonical_name>"`.

---

### `GET /v1/folders?prefix=documents` — List folders

Returns distinct folder paths matching an optional prefix.

```bash
curl "http://localhost:8090/v1/folders?prefix=documents"
```

**Response `200 OK`:**
```json
{
  "folders": ["documents/contracts", "documents/invoices", "documents/pdf", "documents/reports"],
  "count": 4
}
```

---

### `GET /v1/search?q=invoice` — Search files

Searches `original_name`, `canonical_name`, `tags`, and `summary` fields.

```bash
curl "http://localhost:8090/v1/search?q=invoice"
```

**Response `200 OK`:**
```json
{
  "files": [ { ...file record... }, ... ],
  "count": 2
}
```

---

### `GET /health` — Health check

```bash
curl http://localhost:8090/health
# {"status":"ok"}
```

---

## AI Organization

Spectral Vault's organizer works deterministically (no external API keys required). It uses heuristics based on MIME type and filename keywords to:

1. **Auto-name files** with a date prefix: `2026-04-13_invoice_scan.pdf`
2. **Suggest folder paths** based on content type and keywords:

| Trigger | Folder |
|---------|--------|
| MIME `image/*` | `media/images` |
| MIME `video/*` | `media/videos` |
| MIME `audio/*` | `media/audio` |
| MIME `application/pdf` | `documents/pdf` |
| MIME `text/*`, `application/json` | `documents/text` |
| MIME `application/zip`, `x-tar` | `archives` |
| MIME `text/csv`, spreadsheets | `data/spreadsheets` |
| Filename contains `invoice`, `receipt` | `documents/invoices` |
| Filename contains `contract`, `agreement` | `documents/contracts` |
| Filename contains `report` | `documents/reports` |
| Filename contains `backup`, `dump`, `export` | `archives` |
| (default) | `misc` |

3. **Extract tags** from MIME category and keyword matches.
4. **Generate a summary** string for quick identification.

### Pluggable LLM hook

The organizer supports an optional `LLMHook` interface for future LLM integration:

```go
type LLMHook interface {
    Enhance(in organizer.Input, heuristic organizer.Result) (organizer.Result, error)
}

org := organizer.New()
org.SetLLMHook(myLLMImpl)  // plug in your LLM; runs offline if not set
```

No external API keys are required to run Spectral Vault.

---

## Spectral Cloud MQ Integration

On every file upload, `vault-api` publishes an ingest message to Spectral Cloud's MQ:

```
POST {SPECTRAL_URL}/mq/{SPECTRAL_MQ_INGEST_TOPIC}

{
  "payload": {
    "file_id": "vf-3a9f2c1d",
    "tenant": "default",
    "storage_key": "default/vf-3a9f2c1d/invoice_scan.pdf",
    "original_name": "invoice_scan.pdf",
    "sha256": "e3b0c44298fc1c...",
    "mime": "application/pdf",
    "size": 45231
  }
}
```

`vault-worker` polls `GET {SPECTRAL_URL}/mq/{topic}?count=20`, processes each message through the organizer, updates the DB, and publishes results to `{SPECTRAL_MQ_RESULTS_TOPIC}`:

```json
{
  "payload": {
    "file_id": "vf-3a9f2c1d",
    "tenant": "default",
    "canonical_name": "2026-04-13_invoice_scan.pdf",
    "folder_path": "documents/invoices",
    "tags": ["document", "invoice"],
    "status": "organized"
  }
}
```

All MQ calls include Spectral Cloud auth headers (`Authorization: Bearer <key>` or `X-API-Key: <key>`) and are tenant-aware.

---

## Multi-tenant Support

Spectral Vault is tenant-aware:
- Each file is stored under `{DataDir}/{tenant}/...`
- The `tenant` field is part of every DB record and MQ payload
- Tenant is resolved from the `VAULT_TENANT_HEADER` request header (if configured), defaulting to `"default"`
- All DB queries are scoped to the resolved tenant

---

## Running Tests

```bash
cd apps/spectral-vault
go test ./...
```

All packages have unit tests:
- `internal/storage`: filesystem put/get/delete, tenant isolation
- `internal/db`: insert/update/search/dedup/list-folders
- `internal/organizer`: heuristic naming, folder assignment for all MIME types, keyword detection
- `internal/spectralclient`: publish, consume, error handling (uses `httptest`)
