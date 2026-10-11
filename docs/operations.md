# Operating Shop on the Mac mini

Everything runs in Docker from `deploy/docker-compose.yml`:

| Service | What it does |
|---|---|
| `api` | Go server: REST (`/v1`), MCP (`/mcp`), job worker, SQLite in the `shop-data` volume |
| `ocr` | PaddleOCR PP-OCRv5 sidecar (CPU), internal only |
| `backup` | Nightly database snapshot + image mirror to the NAS |

## First install

```bash
git clone <repo> shop && cd shop
export SHOP_BACKUP_DIR=/Volumes/nas/shop   # see "NAS" below
make setup                  # starts the stack and prints the app pairing password once
make token NAME=assistant   # token for your AI assistant (MCP), printed once
```

Check it: `curl localhost:8080/healthz`. Every check should be `ok`.

## Pairing the app

In the app, enter the **server URL** (e.g. `http://mini.<tailnet>.ts.net:8080`) and the **pairing password**:

1. The app calls `GET /v1/auth/status` to check the URL really is a Shop server.
2. It sends the password and the phone's name to `POST /v1/auth/pair`.
3. It gets back its own device token and stores it in the Keychain. The password is not kept.

Each phone gets its own token. Unpair one from the app (Settings → Devices) or with the API (`DELETE /v1/auth/devices/{id}`).

```bash
docker compose -f deploy/docker-compose.yml exec api shop password status                        # set? which devices?
docker compose -f deploy/docker-compose.yml exec api shop password generate                      # new random password
docker compose -f deploy/docker-compose.yml exec -it api shop password set                       # choose your own (≥10 chars)
docker compose -f deploy/docker-compose.yml exec api shop password generate -revoke-devices      # new password + unpair all phones
```

Changing the password does not unpair existing devices unless you add `-revoke-devices`.

Guessing is rate-limited: 5 wrong passwords from one address lock it out for 15 minutes, and 30 failures in an hour pause pairing for everyone. Over Tailscale the password travels encrypted. On a plain LAN it's plain HTTP, so pair at home, or use `tailscale serve` for HTTPS.

## Configuration

Set these as environment variables when running `make up`, or put them in `deploy/.env` (git-ignored; start from `deploy/.env.example`).

| Variable | Default | Meaning |
|---|---|---|
| `SHOP_PORT` | `8080` | Host port for the API + MCP |
| `SHOP_BACKUP_DIR` | `../backups` (repo `backups/`) | Host path of the backup target (NAS mount) |
| `BACKUP_AT` | `03:30` | Daily backup time (Europe/Amsterdam) |
| `BACKUP_KEEP_DAYS` | `30` | Snapshot retention; the newest 3 are always kept |
| `LOGO_DEV_TOKEN` | — | logo.dev publishable key (`pk_…`) for shop logos. Keep it in `deploy/.env` (git-ignored; see `deploy/.env.example`). Without it the app shows monograms |
| `AUTO_PRODUCTS` | `on` | `on`: confirming a receipt links its lines to products or creates new ones (uncertain ones wait in the review queue). `off` disables that and the backfill. Barcode scans always create products |
| `OCR_URL` | `http://ocr:8000` | OCR engine (any service implementing the OCR contract) |
| `OCR_FALLBACK_URL` | — | Second engine to retry `needs_review` receipts with |

## NAS

1. In Finder, connect to the NAS share (`smb://nas.local/backups`) and tick "Remember this password in my keychain".
2. Add the share to **System Settings → General → Login Items** so it mounts after a reboot.
3. Create a folder for Shop on it, e.g. `/Volumes/backups/shop`, and use that as `SHOP_BACKUP_DIR`.
4. In Docker Desktop → Settings → Resources → File sharing, make sure `/Volumes` is shared.

If the NAS isn't mounted, the backup run fails and records why. `/healthz` then reports `backup: last backup failed … backup directory not available` (HTTP 503) until the next good run. The same happens if no backup has succeeded for 36 hours.

```bash
make backup                 # run one now
docker compose -f deploy/docker-compose.yml logs backup
```

What's on the NAS:

```
db/shop-20261010-033000.db    one snapshot per night (consistent, compact)
images/receipts/…             append-only mirror: images are never deleted here,
images/products/…             so older snapshots can always be restored
last_backup.json              status of the latest run
```

## Remote access (Tailscale)

1. Install Tailscale on the Mac mini and the iPhone, and sign both into the same tailnet.
2. Find the mini's name: `tailscale status` (e.g. `mini`).
3. Use `http://mini.<tailnet>.ts.net:8080` as the server URL in the app and for the assistant's MCP endpoint (`…/mcp`).
4. Optional HTTPS: `tailscale serve --bg 8080` serves it at `https://mini.<tailnet>.ts.net`.

No router ports need to be opened.

## Upgrading

```bash
git pull
make up
```

On start, the API applies any new database migrations. If there are migrations to apply, it first writes `pre-migrate-v<old>-<time>.db` next to the database in the `shop-data` volume. That file is your rollback if an upgrade goes wrong, alongside the nightly NAS snapshots.

## Logs and health

- `make logs` follows all services. Docker rotates the logs (10 MB × 3 per service).
- `GET /healthz` (no auth) reports `db`, `ocr`, `jobs` (pending/failed), `storage` (DB size) and `backup`.
- `GET /version` (no auth) reports the build and schema version.

## Tokens (assistant, scripts)

```bash
docker compose -f deploy/docker-compose.yml exec api shop token list
docker compose -f deploy/docker-compose.yml exec api shop token revoke phone
```

## Re-processing receipts

After a parser improvement (a new `parser_version`), re-parse every receipt you haven't edited from its stored OCR output. No OCR run is needed:

```bash
docker compose -f deploy/docker-compose.yml exec api shop reparse
docker compose -f deploy/docker-compose.yml exec api shop reparse -status needs_review
```
