---
applyTo: "pkg/blockchain/*.go,pkg/routing/*.go,cmd/spectral-cloud/*.go,cmd/spectralctl/*.go,docs/api.md,docs/protocol.md"
---

Treat blockchain state and routing state as tenant-owned control-plane data with persistence and API surfaces, not as isolated utility packages.

The blockchain implementation here is intentionally lightweight and append-only. Preserve the current assumptions unless a broader redesign is explicitly intended:

- a generated genesis block always exists
- block hashes are derived from index, timestamp, and transactions
- signatures are optional and separate from the block hash
- invalid persisted chains are filtered or truncated rather than trusted blindly

Routing behavior also has a stable model that callers depend on:

- route destinations are unique identifiers from the caller's perspective
- latency and throughput must remain non-negative
- TTL-based route expiry is part of normal behavior
- satellite routes carry additional selection semantics rather than being a separate route type

If you change blockchain or route payloads, search/filter behavior, or best-route selection semantics, update the matching HTTP handlers, `spectralctl` flows, and docs together.
