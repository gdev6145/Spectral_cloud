# Backup and Restore Runbook

This runbook covers manual backups, encrypted backups, and restore procedures.

## Manual Backup (plaintext)

```bash
spectralctl backup --db-path /var/lib/spectral-cloud/spectral.db --out /var/backups/spectral.db.bak
```

## Manual Backup (encrypted)

```bash
KEY=$(spectralctl keygen)
spectralctl backup --db-path /var/lib/spectral-cloud/spectral.db --out /var/backups/spectral.db.enc --key "$KEY"
```

## Restore (plaintext)

1. Stop the service.
2. Restore the database file.
3. Start the service.

```bash
systemctl stop spectral-cloud
spectralctl restore --db-path /var/lib/spectral-cloud/spectral.db --in /var/backups/spectral.db.bak
systemctl start spectral-cloud
```

## Restore (encrypted)

```bash
systemctl stop spectral-cloud
spectralctl restore --db-path /var/lib/spectral-cloud/spectral.db --in /var/backups/spectral.db.enc --key "$KEY"
systemctl start spectral-cloud
```

## Verification

```bash
spectralctl validate --db-path /var/lib/spectral-cloud/spectral.db
```

## Scheduled Backups

Set `BACKUP_INTERVAL` and `BACKUP_DIR` in the environment file to enable scheduled backups.
