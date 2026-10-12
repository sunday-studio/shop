# Restoring from a backup

Use this when the database is lost or damaged, the machine is replaced, or an upgrade needs rolling back.

You need:
- the repo checked out (`git clone …`)
- a backup folder with a `db/` folder of snapshots in it, with `SHOP_BACKUP_DIR` pointing at it. That is either the folder the nightly backup writes to (a local folder, an external disk or a mounted share), or a copy you brought back from an off-site destination (next section).

## Steps

```bash
cd shop
export SHOP_BACKUP_DIR=/path/to/your/backup-folder

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

## Restoring from an off-site copy

Use this when the backup folder is gone too (a lost machine, a failed disk). It works the same whatever the destination is (S3-compatible store, Backblaze B2, SFTP, WebDAV, Google Drive, Dropbox, a NAS), because rclone does the fetching. If the copy is encrypted (recommended), you need the rclone crypt password and salt you saved when you set it up; without them nothing can be read.

On the new or repaired machine:

```bash
git clone … shop && cd shop
export SHOP_BACKUP_DIR=$HOME/shop-backups      # a new, empty folder
mkdir -p "$SHOP_BACKUP_DIR"

# 1. Tell rclone how to reach the destination, exactly as before:
#    put your rclone.conf in deploy/offsite/config/, or the RCLONE_CONFIG_* lines in
#    deploy/offsite/offsite.env, and set the destination name.
export OFFSITE_REMOTE=shopcrypt:shop

# 2. Look at what is there (optional), then bring it back into the backup folder.
docker compose -f deploy/docker-compose.yml --profile offsite run --rm offsite list
make offsite-pull          # same as: docker compose … --profile offsite run --rm offsite pull

# 3. Restore from that folder, as in "Steps" above (stop the stack first if it is running).
docker compose -f deploy/docker-compose.yml run --rm --no-deps backup \
  restore -yes -snapshot latest
make up
curl localhost:8080/healthz
```

`pull` stops with an error if no snapshot came back, so an empty or wrong destination does not look like a success. Without Docker you can do step 2 with rclone itself: `rclone copy shopcrypt:shop/db "$SHOP_BACKUP_DIR/db"` and `rclone copy shopcrypt:shop/images "$SHOP_BACKUP_DIR/images"`.

`/healthz` has no off-site status right after a restore, because that status lives in the data volume that was just replaced; it comes back after the next backup and copy.

## Test it without risk

`make restore-drill` runs the whole thing automatically in a separate Docker stack (compose project `shop-drill`, port 18481, throwaway volumes; your real stack, port 8080 and data are never touched). It creates receipts with images, products, categories and list items, takes a backup, wipes that stack's data volume, restores, and compares the counts and a hash of every receipt image. Then it copies the backup to an encrypted rclone destination (a throwaway folder), deletes both the data volume and the backup folder, pulls the copy back, restores again and compares again. It prints PASS or FAIL per step and exits non-zero on any failure. It needs Docker and, the first time, a network connection to fetch the `rclone/rclone` image.

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
| 2026-10-12 | `make restore-drill` (isolated stack): 3 receipts with images, products, categories, list. Restored from the backup folder, then from an encrypted rclone copy after losing the data volume and the backup folder. | Counts, image hashes and the device token identical both times. The destination was a throwaway folder; a real cloud destination was not exercised. |
| 2026-10-10 | Seeded 2 receipts + a product alias, ran `make backup`, deleted the `shop-data` volume with `docker compose down -v`, restored `latest`, restarted. | Spend total (€10,36), receipt count, alias and receipt images all identical; the existing API token still worked. The `/healthz` backup check comes back after the next nightly run, because its status file lives in the data volume. |

Run `make restore-drill` after upgrades that touch backups, and repeat the manual drill on the real stack every few months and after changing where backups go, using a spare machine or a copy of the folder, not the live data.
