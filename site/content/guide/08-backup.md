---
title: Update, back up and restore
group: Self-hosting
summary: Keep the server current and your data safe.
---

## Update

```bash
git pull
make up
```

When the server starts, it applies any new database migration. Before it does, it saves a copy of the database named `pre-migrate-v<N>-<time>.db` in the data volume. That copy is your way back if an update goes wrong.

## Back up

A backup service saves a snapshot of the database every night and mirrors the images to a folder. Point that folder at another disk or a network share, so a copy lives off the machine.

1. Mount the disk or network share on the machine, and make sure Docker is allowed to use that path.
2. Set `SHOP_BACKUP_DIR` in `deploy/.env`, for example `/Volumes/backups/shop`.
3. Start the stack again, then run a backup now to check it works.
   ```bash
   make up
   make backup
   ```

Backups run at 03:30 by default and snapshots are kept for 30 days; the newest three are always kept. If the folder is not available, the next run fails and the app shows **NAS backup is out of date** in Settings.

## Restore

You need the repo and your backup folder.

1. Stop the stack.
   ```bash
   docker compose -f deploy/docker-compose.yml down
   ```
2. Restore the latest snapshot. This checks the snapshot, replaces the database and copies back any missing images.
   ```bash
   docker compose -f deploy/docker-compose.yml run --rm --no-deps backup restore -yes -snapshot latest
   ```
3. Start again and check.
   ```bash
   make up
   curl localhost:8080/healthz
   ```

Phones and tokens keep working after a restore, because they are stored in the database.

To go back after a failed update, use the `pre-migrate` copy instead. The steps are in the [restore guide](https://github.com/sunday-studio/shop/blob/main/docs/restore.md).
