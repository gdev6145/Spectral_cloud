---
name: spectral-cloud-mesh-proto
description: Guidance for changing or debugging Spectral_cloud mesh, gRPC, protobuf, and TLS flows. Use when working on proto definitions, mesh transport, mesh CLI commands, or protobuf HTTP endpoints.
---

# Spectral Cloud mesh and protobuf workflow

Use this skill when a task involves the mesh protocol or protobuf-backed interfaces.

## Relevant files and areas

- `proto/`: protobuf definitions such as mesh messages and services
- `pkg/mesh` and `pkg/proto`: mesh transport and protobuf handling
- `cmd/spectral-cloud`: service-side mesh and HTTP wiring
- `cmd/spectralctl`: client-side tools such as `mesh-send`, `mesh-watch`, and `mesh-load`
- `docs/protocol.md`, `docs/api.md`, and `docs/mesh-tls.md`: protocol and usage references

## Expected workflow

1. Start from the protocol definition and trace both server and CLI/client handling before making a change.
2. If you edit `.proto` files, regenerate outputs with `./scripts/gen-proto.sh`.
3. Update user-facing docs when message formats, endpoint behavior, TLS flags, or CLI usage changes.
4. Preserve existing support for protobuf HTTP endpoints:
   - `POST /proto/data`
   - `POST /proto/control`
5. Preserve or explicitly update the mesh CLI flows documented in `README.md` and `docs/api.md`.

## Useful local checks

- Regenerate protobufs: `./scripts/gen-proto.sh`
- Run tests: `go test ./...`
- Exercise mesh commands with `spectralctl`, for example:
  - `go run ./cmd/spectralctl mesh-send --addr localhost:9091 --api-key devkey --tenant default --kind data --payload "hello"`
  - `go run ./cmd/spectralctl mesh-watch --addr localhost:9091 --api-key devkey --tenant default --interval 5s`
  - `go run ./cmd/spectralctl mesh-load --addr localhost:9091 --api-key devkey --tenant default --kind data --count 100`

## TLS notes

When a task involves secure mesh traffic, consult `docs/mesh-tls.md` and preserve support for the existing TLS flags:

- `--tls`
- `--tls-ca`
- `--tls-server-name`
- `--tls-insecure-skip-verify`
