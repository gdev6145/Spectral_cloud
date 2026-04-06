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
- Some vulnerabilities require upgrading the Go toolchain and/or gRPC. If your environment is pinned to an older toolchain, schedule an upgrade to a patched Go version as soon as feasible.
