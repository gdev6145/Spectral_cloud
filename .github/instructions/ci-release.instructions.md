---
applyTo: ".github/workflows/*.yml,.github/workflows/*.yaml,Dockerfile,spectral-cloud/Chart.yaml,spectral-cloud/values.yaml"
---

Preserve the coupling between CI checks, release artifacts, container publishing, and Helm packaging.

CI is the source of truth for validation expectations in this repository. If you change build or generation behavior, keep `.github/workflows/ci.yml` aligned with the documented local commands and with any generated-file expectations.

The release workflow currently ties together:

- cross-platform `spectral-cloud` and `spectralctl` binaries in `dist/`
- GHCR image publishing for `ghcr.io/<owner>/spectral-cloud`
- Helm chart version and `appVersion` updates in `spectral-cloud/Chart.yaml`

If you change binary names, image names, chart paths, or versioning behavior, update the release workflow and the corresponding docs together. Do not make a local packaging change that only updates one of those surfaces.

When editing workflow commands, preserve their non-interactive behavior and artifact paths so release automation and consumers downstream do not break unexpectedly.
