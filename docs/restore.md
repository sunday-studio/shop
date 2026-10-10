# Restoring from a backup

Use this when the database is lost or damaged, the Mac mini is replaced, or an upgrade needs rolling back.

You need:
- the repo checked out (`git clone …`)
- the NAS mounted, with `SHOP_BACKUP_DIR` pointing at the Shop folder on it

## Steps

```bash
cd shop
export SHOP_BACKUP_DIR=/Volumes/backups/shop

# 1. Stop the stack. The API must not be writing while the DB is replaced.
docker compose -f deploy/docker-compose.yml down

# 2. See what's available (newest last).
ls "$SHOP_BACKUP_DIR/db"

# 3. Restore. "latest" or a file name from step 2.
#    This checks the snapshot's integrity, replaces /data/shop.db,
#    and copies back any images missing from the data volume.
docker compose -f deploy/docker-compose.yml run --rm --no-deps backup \
  restore -yes -snapshot latest

# 4. Start again and check.
make up
curl localhost:8080/healthz
```

API tokens are stored in the database, so existing clients keep working after a restore.

## Rolling back a failed upgrade

The API writes `pre-migrate-v<N>-<time>.db` into the data volume before it migrates. To go back to it:

```bash
docker compose -f deploy/docker-compose.yml down
git checkout <previous version>
docker run --rm -v shop_shop-data:/data alpine sh -c \
  'ls /data/pre-migrate-*.db && cp /data/pre-migrate-v<N>-<time>.db /data/shop.db && rm -f /data/shop.db-wal /data/shop.db-shm'
make up
```

## Drill log

| Date | What | Result |
|---|---|---|
| 2026-10-10 | Seeded 2 receipts + a product alias, ran `make backup`, deleted the `shop-data` volume with `docker compose down -v`, restored `latest`, restarted. | Spend total (€10,36), receipt count, alias and receipt images all identical; the existing API token still worked. The `/healthz` backup check comes back after the next nightly run, because its status file lives in the data volume. |

Repeat the drill every few months, and after changing the NAS setup.
