---
applyTo: "spectral-cloud/Chart.yaml,spectral-cloud/values.yaml,spectral-cloud/templates/**/*.yaml"
---

The `spectral-cloud/` directory is the repository's Helm chart. Keep chart metadata, default values, templates, and the published release flow in sync.

If you change container image defaults, ports, probes, persistence wiring, or environment variable names in the chart, update the matching deployment docs and any release guidance that references them.

Prefer surfacing application configuration through `values.yaml` rather than hardcoding values in templates. Template changes should preserve a deployable default installation.

When chart behavior changes, check whether `README.md`, `docs/configuration.md`, or release automation expectations also need updates.
