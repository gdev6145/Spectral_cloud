---
applyTo: "pkg/auth/*.go,docs/configuration.md,README.md,cmd/spectral-cloud/*.go"
---

Auth and access control are configured by environment variables and path rules, not by one-off handler logic. Prefer preserving or extending the existing auth model instead of adding route-specific exceptions.

Keep the credential precedence and tenant resolution behavior consistent across HTTP and gRPC flows:

- admin keys
- tenant write keys
- tenant keys
- write key
- API key

`TENANT_KEYS` and `TENANT_WRITE_KEYS` use `tenant:key` pairs. Parsing is intentionally strict; invalid or empty entries should continue to fail clearly instead of being silently ignored.

HTTP auth supports both `Authorization: Bearer ...` and `X-API-Key`, and gRPC tenant resolution reads matching metadata keys. Changes to supported credential formats need coordinated updates in `pkg/auth`, server auth middleware, and docs.

If you change `PUBLIC_PATHS`, `ADMIN_PATHS`, admin CIDR behavior, or any auth environment variable semantics, update `docs/configuration.md` and the user-facing examples in `README.md` in the same change.
