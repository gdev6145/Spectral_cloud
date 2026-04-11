---
name: spectral-cloud-development
description: Guidance for implementing and reviewing general code changes in the Spectral_cloud repository. Use when working on Go code, HTTP handlers, storage, auth, routing, tests, or docs in this project.
---

# Spectral Cloud development workflow

Use this skill for everyday repository work in `Spectral_cloud`.

## Repository map

- `cmd/spectral-cloud`: main service entrypoint
- `cmd/spectralctl`: operational CLI for validation, backup, compaction, key management, and mesh testing
- `pkg/`: application packages such as `auth`, `blockchain`, `mesh`, `routing`, `store`, `healthcheck`, and `proto`
- `docs/`: architecture, API, configuration, protocol, TLS, and operations documentation
- `scripts/gen-proto.sh`: regenerate protobuf outputs when `.proto` files change

## Working rules

1. Keep changes focused and consistent with existing package boundaries. Prefer extending the relevant package in `pkg/` over adding duplicate helpers elsewhere.
2. When behavior changes, update the related docs in `docs/` or `README.md`.
3. If an API surface changes, check `docs/api.md`, `docs/configuration.md`, and any affected operational docs.
4. If protobuf definitions change, run `./scripts/gen-proto.sh` and make sure generated files are included in the change.
5. Follow the repository contribution guidance: add or update tests for behavior changes and keep naming/configuration explicit.

## Validation checklist

When your change touches code, prefer the same checks used by CI:

1. `test -z "$(gofmt -l .)"`
2. `go test ./...`
3. Run `./scripts/gen-proto.sh` if protobufs were modified

If you change code paths related to `spectralctl`, also exercise the relevant command locally instead of relying on tests alone.
