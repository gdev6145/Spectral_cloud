# Contributing

Thanks for your interest in Spectral-Cloud. This guide covers the basics to get changes merged quickly.

## Development Setup

```bash
go test ./...
```

Optional:
- `./scripts/gen-proto.sh` to regenerate protobufs

## Workflow
1. Fork and create a feature branch.
2. Keep changes focused and well-scoped.
3. Update docs when behavior changes.
4. Add or update tests for new behavior.
5. Ensure `go test ./...` is clean.

## Copilot skills

This repository includes project-level Copilot skills under `.github/skills/` for:

- general Spectral Cloud development
- mesh and protobuf changes
- operations and debugging

If you add or edit a skill during a live Copilot CLI session, run `/skills reload` so the CLI picks up the new definitions without restarting.

## Code Style
- Prefer small, composable functions.
- Avoid hidden side effects.
- Use meaningful names for configuration and metrics.

## Pull Requests
Please include:
- A brief summary of the change
- Tests run
- Any breaking changes or migration notes

## Security
Do not file security issues publicly. Follow `SECURITY.md`.
