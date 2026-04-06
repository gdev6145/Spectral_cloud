# Systemd Deployment

This provides a baseline systemd unit and optional log rotation config.

## 1. Install the binary

```bash
install -m 0755 ./spectral-cloud /usr/local/bin/spectral-cloud
```

## 2. Environment file

Create `/etc/spectral-cloud.env`:

```bash
PORT=8080
DATA_DIR=/var/lib/spectral-cloud
BACKUP_INTERVAL=1h
BACKUP_DIR=/var/backups/spectral-cloud
```

## 3. Service unit

Copy `docs/ops/spectral-cloud.service` to `/etc/systemd/system/spectral-cloud.service`.

Then:

```bash
systemctl daemon-reload
systemctl enable --now spectral-cloud
```

## 4. Logs

By default, logs go to journald. If you prefer log files, use the provided logrotate config and uncomment the `StandardOutput/StandardError` lines in the service unit.

Copy `docs/ops/spectral-cloud.logrotate` to `/etc/logrotate.d/spectral-cloud`.
