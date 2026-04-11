# Copilot Instructions for Spectral_cloud

## Build, test, and lint

- Full test suite: `go test ./...`
- Run a single package: `go test ./pkg/store`
- Run a single test: `go test ./pkg/store -run TestBackupEncrypted` or `go test ./cmd/spectral-cloud -run TestHealth`
- Format check used in CI: `test -z "$(gofmt -l .)"`
- Lint check used in CI: `golangci-lint run --timeout=5m`
- Regenerate protobuf outputs after changing `proto/mesh.proto`: `./scripts/gen-proto.sh`
- Local binary builds:
  - `go build ./cmd/spectral-cloud`
  - `go build ./cmd/spectralctl`
  - `go build ./cmd/spectral-agent`
- Local stack with observability: `docker compose up --build`

CI also runs `govulncheck ./...` and smoke-tests `spectralctl validate`, `backup`, and `rekey` flows.

## High-level architecture

- `cmd/spectral-cloud` is the main control plane. Startup opens BoltDB via `pkg/store`, migrates legacy `data/blockchain.json` and `data/routes.json` into BoltDB on first run, then constructs runtime managers for agents, jobs, schedules, notifications, circuit breaking, health checks, and webhooks.
- Multi-tenancy is core to the runtime model. `cmd/spectral-cloud/tenant.go` keeps a lazy in-memory cache of per-tenant `blockchain.Blockchain` and `routing.RoutingEngine` instances, while `pkg/store` persists tenant data under BoltDB tenant buckets.
- HTTP handling is centralized in `newHandler` in `cmd/spectral-cloud/main.go`. Most handlers call `getState(r)` to resolve the tenant from request context and then work against that tenant's chain/router state.
- Middleware order matters: auth -> audit logging -> tenant rate limiting -> IP rate limiting -> Prometheus metrics/duration -> CORS -> optional access log -> request ID -> panic recovery.
- `pkg/store` is the system of record, but several higher-level subsystems persist through its KV helpers:
  - `pkg/jobs` stores `job_*`
  - `pkg/scheduler` stores `sched_*`
  - agent groups and notification rules also restore from BoltDB on startup
- `pkg/mesh` and `pkg/proto` back the mesh/protobuf surfaces. `proto/mesh.proto` generates Go code into `pkg/proto`, the server exposes `/proto/data` and `/proto/control`, and `cmd/spectralctl` provides `mesh-send`, `mesh-watch`, and `mesh-load` for exercising those paths.
- `cmd/spectral-agent` is a standalone worker process that registers with the control plane, heartbeats, polls `/agents/jobs`, and executes capabilities. It is not a separate library package.
- There is no separate frontend build. The dashboard is embedded into `cmd/spectral-cloud` and served from `/ui/`.
- Deployment assets live alongside the Go service:
  - `docker-compose.yml` brings up multiple nodes plus Prometheus and Alertmanager
  - `spectral-cloud/` contains the Helm chart

## Key conventions

- Keep business logic in `pkg/*`; keep HTTP/server wiring in `cmd/spectral-cloud`; keep operator tooling in `cmd/spectralctl`.
- Tenant scoping is not optional. When changing handlers or persistence, preserve the flow where auth resolves the tenant, context carries it, and reads/writes stay tenant-specific unless the endpoint is explicitly admin-scoped.
- Auth behavior is driven by environment-configured path rules, not scattered per-handler checks:
  - `PUBLIC_PATHS` controls unauthenticated routes
  - `ADMIN_PATHS` controls admin routes
  - `TENANT_KEYS` / `TENANT_WRITE_KEYS` switch the server into tenant-key auth
- Error responses are consistently JSON via `writeError`, with shape `{"error":"..."}`.
- If you change protobuf definitions, regenerate with `./scripts/gen-proto.sh` and commit the generated files under `pkg/proto`; CI checks for a clean diff after generation.
- When changing API behavior or config surfaces, update the matching docs in the same change. The most relevant references are `docs/api.md`, `docs/configuration.md`, `docs/protocol.md`, `docs/mesh-tls.md`, and `docs/ops/*`.
- Persistent runtime features must keep restart behavior in mind. Jobs, schedules, agent groups, and notification rules are restored from BoltDB during startup, so storage format changes need backward-compatible load behavior.
- If you edit files under `.github/skills/` during a live Copilot CLI session, run `/skills reload` so the updated project skills are picked up without restarting the session.
- This repository also uses path-specific instruction files under `.github/instructions/` for focused areas such as the control plane entrypoint, mesh/protobuf flows, persistence/runtime state, agent/job handling, deployment assets, tests, docs, and CI/release wiring.

## MCP setup

- The repository includes a shareable Copilot CLI MCP template at `.copilot/mcp-config.json`.
- It is scoped to the workflows already present in this repo:
  - `docker-local` for local container inspection
  - `kubernetes-non-destructive` for cluster and Helm work without destructive delete operations
  - `prometheus-local` targeting `http://localhost:9090`
  - `alertmanager-local` targeting `http://localhost:9093`
- The Prometheus and Alertmanager entries match the local endpoints exposed by `docker-compose.yml`.
- Prefer local MCP binaries when available:
  - `mcp-server-docker`
  - `prometheus-mcp-server`
  - `alertmanager-mcp-server`
- For Copilot CLI, copy or merge these entries into `~/.copilot/mcp-config.json`, or recreate them interactively with `/mcp add`.
