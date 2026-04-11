---
applyTo: "scripts/*.sh"
---

Treat scripts in this repository as operator/developer entrypoints, not throwaway helpers. Keep them safe for repeated local use and aligned with the commands documented in `README.md` and `docs/`.

`scripts/gen-proto.sh` is part of the repository contract: if protobuf generation changes, keep its tool checks and output paths aligned with CI and the generated files under `pkg/proto`.

`scripts/gen-mesh-certs.sh` supports documented mesh/TLS workflows. If you change generated filenames, certificate subjects, or output layout, update the related TLS docs and examples in the same change.

Prefer explicit failure with clear prerequisites over silent fallback behavior in scripts.
