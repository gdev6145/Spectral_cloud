# Security Policy

## Reporting a Vulnerability

Please report security issues privately.

Preferred: open a GitHub Security Advisory for this repository.
Alternative: email the maintainers at security@spectral-cloud.local (replace with a real address).

Include:
- A clear description of the issue
- Steps to reproduce
- Impact assessment
- Suggested remediation (if known)

## Supported Versions

Only the latest release is supported with security updates.

## Dependency Security

- Run `govulncheck ./...` regularly and review results.
- Keep the Go toolchain current with the `go.mod` `toolchain` directive and the CI/Docker builder versions. Security fixes in `grpc` and the Go standard library may require coordinated upgrades.
