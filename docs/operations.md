# Operating Shop

Shop runs at home (a Mac mini is the reference machine, any computer that runs Docker works). Everything runs in Docker from `deploy/docker-compose.yml`. Nothing in it needs a particular NAS, cloud or VPN: backups go to a folder you choose, and an off-site copy and remote access are optional add-ons you can pick freely.

| Service | What it does |
|---|---|
| `api` | Go server: REST (`/v1`), MCP (`/mcp`), job worker, SQLite in the `shop-data` volume |
| `ocr` | PaddleOCR PP-OCRv5 sidecar (CPU), internal only |
| `backup` | Nightly database snapshot + image mirror into a backup folder |
| `offsite` | Optional (profile `offsite`): copies the backup folder to a destination of your choice with rclone |

## First install

```bash
git clone <repo> shop && cd shop
export SHOP_BACKUP_DIR=$HOME/shop-backups   # optional; any folder or mounted drive, see "Backups" below
make setup                  # starts the stack and prints the app pairing password once
make token NAME=assistant   # token for your AI assistant (MCP), printed once
```

Check it: `curl localhost:8080/healthz`. Every check should be `ok`.

## Pairing the app

In the app, enter the **server URL** (see "Remote access" below for what to type) and the **pairing password**:

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

Guessing is rate-limited: 5 wrong passwords from one address lock it out for 15 minutes, and 30 failures in an hour pause pairing for everyone. The password is only as private as the connection: over a VPN or HTTPS it travels encrypted. On a plain home network it is plain HTTP, so pair at home or over one of the options in "Remote access". Behind a proxy or tunnel every phone appears to come from the proxy's address, so five wrong guesses can pause pairing for everyone for 15 minutes; that is a nuisance, not a way in.

## Configuration

Set these as environment variables when running `make up`, or put them in `deploy/.env` (git-ignored; start from `deploy/.env.example`).

| Variable | Default | Meaning |
|---|---|---|
| `SHOP_PORT` | `8080` | Host port for the API + MCP |
| `SHOP_BACKUP_DIR` | `../backups` (repo `backups/`) | Host path of the backup folder: a local folder, an external disk or a network mount |
| `BACKUP_AT` | `03:30` | Daily backup time (Europe/Amsterdam) |
| `BACKUP_KEEP_DAYS` | `30` | Snapshot retention; the newest 3 are always kept |
| `OFFSITE_REMOTE` | — | Optional off-site destination, an rclone remote and path such as `shopcrypt:shop`. Empty: no off-site copy |
| `OFFSITE_PRUNE` | `on` | `on`: old snapshots are also removed at the destination. `off`: only ever add |
| `OFFSITE_RCLONE_FLAGS` | — | Extra rclone flags, e.g. `--bwlimit 2M` |
| `OFFSITE_LABEL` | name before the first `:` | Name shown in `/healthz` for the off-site copy. Never put secrets in it |
| `LOGO_DEV_TOKEN` | — | logo.dev publishable key (`pk_…`) for shop logos. Keep it in `deploy/.env` (git-ignored; see `deploy/.env.example`). Without it the app shows monograms |
| `AUTO_PRODUCTS` | `on` | `on`: confirming a receipt links its lines to products or creates new ones (uncertain ones wait in the review queue). `off` disables that and the backfill. Barcode scans always create products |
| `OCR_URL` | `http://ocr:8000` | OCR engine (any service implementing the OCR contract) |
| `OCR_FALLBACK_URL` | — | Second engine to retry `needs_review` receipts with |

## Backups

The default needs nothing: the `backup` service writes to `SHOP_BACKUP_DIR`, which falls back to the `backups/` folder in the repo. That keeps the data safe from a broken database, but not from losing the machine, so point it at a different disk or add the off-site copy below. Good choices for the folder:

- an external drive that stays plugged in,
- a network share (NAS, another computer) mounted on the host,
- a synced folder (any sync tool), as long as it is a normal folder on the host.

If the folder is on a drive or share, make sure it is mounted after a reboot, and that Docker may use the path (Docker Desktop: Settings, Resources, File sharing; on macOS add `/Volumes`). A network share on macOS: connect in Finder, tick "Remember this password in my keychain", and add it to System Settings, General, Login Items.

If the folder is not available, the backup run fails and records why. `/healthz` then reports `backup: last backup failed … backup directory not available` (HTTP 503) until the next good run. The same happens if no backup has succeeded for 36 hours.

```bash
make backup                 # run one now
docker compose -f deploy/docker-compose.yml logs backup
```

What is in the folder:

```
db/shop-20261010-033000.db    one snapshot per night (consistent, compact), kept BACKUP_KEEP_DAYS days
images/receipts/…             append-only mirror: images are never deleted here,
images/products/…             so older snapshots can always be restored
last_backup.json              status of the latest run
```

## Off-site copy (optional)

The off-site copy is a second step after the backup: it copies the backup folder to another place, using [rclone](https://rclone.org). rclone speaks to S3-compatible stores (AWS, Wasabi, MinIO and others), Backblaze B2, SFTP, WebDAV, Google Drive, Dropbox, a mounted NAS, or just another folder. You choose; Shop does not care which. Without it nothing changes and nothing breaks.

What it does, after every successful backup (and again 30 minutes after a failure):

1. copies new images, then the snapshots (so a snapshot never arrives before its images),
2. checks that every local snapshot exists at the destination with the same size,
3. records the result in `offsite_status.json`, which `/healthz` includes in its `backup` check. If the last copy failed, or none succeeded for 36 hours, `/healthz` reports it (HTTP 503), the same way as a failed local backup. The destination is shown only by the label you set, never by the connection details.

### Set it up

1. Create the destination, then describe it to rclone. Either put an `rclone.conf` in `deploy/offsite/config/`, or set `RCLONE_CONFIG_<NAME>_…` variables in `deploy/offsite/offsite.env`. Both are git-ignored. The easiest way to make an `rclone.conf` is `rclone config` on any computer, then copy the file over.
2. **Encrypt anything that leaves your home.** Receipts are private (shops, times, what you buy, photos). Add an rclone `crypt` remote that wraps your real destination, and point Shop at the crypt remote. File names and contents are then encrypted before they leave the machine; the provider only sees opaque files. Keep the crypt password and salt somewhere other than this computer (a password manager): without them the copy cannot be read, by anyone.
3. Set `OFFSITE_REMOTE` (in `deploy/.env` or your shell) and start it:

```bash
# deploy/.env
OFFSITE_REMOTE=shopcrypt:shop

make offsite-up            # starts the offsite service; it copies after each backup
make offsite-sync          # or copy right now and see the result
```

Example `deploy/offsite/offsite.env` for an S3-compatible bucket, encrypted, with no config file at all (the `crypt` password must be in rclone's obscured form: run `docker run --rm rclone/rclone obscure 'your password'`):

```
RCLONE_CONFIG_STORE_TYPE=s3
RCLONE_CONFIG_STORE_PROVIDER=Other
RCLONE_CONFIG_STORE_ENDPOINT=https://s3.example.com
RCLONE_CONFIG_STORE_ACCESS_KEY_ID=...
RCLONE_CONFIG_STORE_SECRET_ACCESS_KEY=...
RCLONE_CONFIG_SHOPCRYPT_TYPE=crypt
RCLONE_CONFIG_SHOPCRYPT_REMOTE=store:my-bucket/shop
RCLONE_CONFIG_SHOPCRYPT_PASSWORD=<obscured password>
RCLONE_CONFIG_SHOPCRYPT_PASSWORD2=<obscured salt>
```

Other destinations are the same pattern with a different `TYPE` (`b2`, `sftp`, `webdav`, `drive`, `dropbox`, `local`). See the rclone documentation for each. A mounted NAS or another disk needs no rclone configuration at all: mount it on the host, set `OFFSITE_REMOTE=:local:/Volumes/other-disk/shop`, and add the folder as a volume of the `offsite` service (or just use it as `SHOP_BACKUP_DIR`).

`OFFSITE_PRUNE=on` (default) removes old snapshots at the destination as the local ones expire. Set `off` if you want the destination to keep everything (it then grows). Images are never removed at the destination.

Useful commands:

```bash
docker compose -f deploy/docker-compose.yml --profile offsite logs offsite
docker compose -f deploy/docker-compose.yml --profile offsite run --rm offsite list   # what is at the destination
make offsite-down
```

Restoring from the off-site copy is in [restore.md](restore.md).

## Remote access

You only need this to use the app, or your AI assistant, away from home. The app needs one thing: a **server URL** that reaches the Shop API.

How the app connects:

- At home, use the machine's local address with plain http, for example `http://192.168.1.20:8080` or `http://mini.local:8080`. The app allows plain http only for local-network addresses (and, for convenience, `*.ts.net` names).
- Anywhere else use an `https://` address. iOS blocks plain http to other hosts.
- The pairing password and the device token travel in every request. Over a VPN (Tailscale, WireGuard) the traffic is already encrypted, so http inside the VPN is fine. Over the open internet it must be https.

**What to avoid:** do not forward port 8080 from your router, and do not publish it on a server's public address, with plain http. Anyone on the path could read the pairing password and the tokens, and the port would be open to password guessing from the whole internet. If you want it reachable from the internet, put https in front of it (Cloudflare Tunnel or Caddy below) and keep the port itself on `127.0.0.1`.

Pick whichever fits; nothing here depends on one provider, and you can change your mind later (the app just needs the new server URL).

| Option | Good when | Server URL in the app |
|---|---|---|
| **Tailscale** | You want the least setup. Install it on the host and the phone, same account. | `http://<machine>.<tailnet>.ts.net:8080`, or `https://<machine>.<tailnet>.ts.net` after `tailscale serve --bg 8080` (HTTPS certificates must be enabled for the tailnet) |
| **Any VPN (WireGuard, OpenVPN, your router's VPN)** | You already run one, or want no third party. Phone and host are on the same VPN. | `http://<host's VPN address>:8080` (http is fine inside the VPN if the address is a local-network address; otherwise add https with a proxy) |
| **Cloudflare Tunnel** | You have a domain on Cloudflare and want a public https name with no open ports. | `https://shop.example.com` |
| **Reverse proxy with automatic HTTPS (Caddy) on a VPS** | You have a server with a public address, or a VPS joined to your home machine by a VPN. | `https://shop.example.com` |

Optional example compose files, all without secrets (keys come from your shell) and none required, are in `deploy/remote/`:

```bash
# Tailscale in a container (instead of installing it on the host)
export TS_AUTHKEY=...      # one-off key from your Tailscale admin console
docker compose -f deploy/docker-compose.yml -f deploy/remote/docker-compose.tailscale.example.yml up -d

# Cloudflare Tunnel (public hostname http://api:8080 set in the Cloudflare dashboard)
export CLOUDFLARE_TUNNEL_TOKEN=...
docker compose -f deploy/docker-compose.yml -f deploy/remote/docker-compose.cloudflared.example.yml up -d

# Caddy with automatic HTTPS
export SHOP_DOMAIN=shop.example.com
docker compose -f deploy/docker-compose.yml -f deploy/remote/docker-compose.caddy.example.yml up -d
```

The Cloudflare and Caddy examples move the API's port to `127.0.0.1` so that the only way in from outside is the https front. For WireGuard or another VPN there is nothing to add to compose: install the VPN on the host and phone, and use the host's VPN address. The assistant's MCP endpoint is the same URL with `/mcp` added, plus its token.

## Upgrading

```bash
git pull
make up
```

On start, the API applies any new database migrations. If there are migrations to apply, it first writes `pre-migrate-v<old>-<time>.db` next to the database in the `shop-data` volume. That file is your rollback if an upgrade goes wrong, alongside the nightly snapshots.

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
