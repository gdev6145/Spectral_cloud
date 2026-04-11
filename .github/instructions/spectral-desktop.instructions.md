---
applyTo: "cmd/spectral-desktop/**/*.go"
---

`cmd/spectral-desktop` is a launcher, not a second control-plane implementation. Keep it thin: it should translate flags into environment variables, start `spectral-cloud`, wait for readiness, and open the dashboard.

Preserve compatibility with the real server binary contract. If you change how `spectral-desktop` discovers or starts `spectral-cloud`, make sure the usage text and `README.md` stay accurate.

Avoid duplicating HTTP handlers, persistence logic, or configuration parsing here. Those behaviors should continue to live in `cmd/spectral-cloud` and shared packages.

Cross-platform browser-launch behavior matters in this package; keep Linux, macOS, and Windows handling explicit when changing startup UX.
