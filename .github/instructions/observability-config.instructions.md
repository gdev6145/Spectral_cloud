---
applyTo: "prometheus/**/*.yml,prometheus/**/*.yaml,alertmanager/**/*.yml,alertmanager/**/*.yaml,alertmanager/**/*.sh"
---

The observability configs in `prometheus/` and `alertmanager/` are part of the repository's runnable local stack. Keep them consistent with `docker-compose.yml`, exposed service ports, and the metrics or alert labels emitted by `cmd/spectral-cloud`.

When editing Prometheus rules, preserve the relationship between alert expressions, metric names, and the annotations rendered to downstream receivers. If a metric or label name changes in Go code, update the matching alert rules in the same change.

`alertmanager/alertmanager.tmpl.yml` is the environment-rendered source used by the compose setup. Keep `render.sh`, the template, and any checked-in example configs aligned so operators can understand both the generated and full forms.

Prefer operationally useful defaults and explicit placeholders over hidden assumptions about local secrets or third-party endpoints.
