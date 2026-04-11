---
applyTo: "start-spectral.sh"
---

`start-spectral.sh` is a developer convenience launcher for starting a local server and opening the embedded UI. Keep it aligned with the actual binary flags, default port, and `/ui/` path used by `spectral-cloud`.

If you change binary lookup, data directory behavior, readiness checks, or browser-launch logic here, make sure the script still works as a low-friction local entrypoint rather than assuming a full deployment environment.

Prefer explicit failures and simple local defaults over hidden setup. This script should remain understandable to someone bootstrapping the project on a workstation.
