---
applyTo: "proto/*.proto,pkg/proto/*.go,pkg/mesh/*.go,cmd/spectralctl/*.go,docs/protocol.md,docs/api.md,docs/mesh-tls.md"
---

Keep the mesh and protobuf surfaces aligned across protocol definitions, generated Go code, server handlers, CLI flows, and docs.

If a `.proto` file changes, regenerate outputs with `./scripts/gen-proto.sh` and keep generated files under `pkg/proto` in the same change. CI expects generated files to already be committed.

Preserve the existing mesh-facing HTTP and CLI surfaces unless the change intentionally updates them together:

- `POST /proto/data`
- `POST /proto/control`
- `spectralctl mesh-send`
- `spectralctl mesh-watch`
- `spectralctl mesh-load`

When changing mesh TLS or transport behavior, keep the documented flags and environment variables in sync with the implementation, especially the TLS-related `spectralctl` flags and the mesh environment variables in `docs/configuration.md` and `docs/mesh-tls.md`.
