# Hosted Shop: server runbook

For the paid hosted version on one fresh Linux server (UpCloud, EU, Ubuntu 24.04 LTS or Debian 12 assumed). Everything is in `deploy/hosted/`. The home install (`deploy/docker-compose.yml`) is a different thing; this kit never touches it. Capacity and sizing: [CAPACITY.md](CAPACITY.md).

Scripts: `bootstrap.sh` (once, as root), `deploy.sh` (day-to-day, as the deploy user), `smoke.sh`, `compose.sh` (the only door to docker compose; project `shop-hosted`), `monitor/` (alerts).

## Before day one (owner)

1. A server with 8 GB RAM, 4 vCPU, 80 GB disk or more (see CAPACITY.md; 4 GB is the bare minimum). Note the public IP.
2. A DNS record: `shop.<your domain>` A (and AAAA) pointing at that IP. Caddy cannot get a certificate before this resolves.
3. The Apple values from `api/internal/hosted/APPLE.md` (team id, app id, the two key ids and the issuer id, the two `.p8` keys, the subscription product id). The server does not start in production without them.
4. An S3 bucket for backups, e.g. UpCloud Managed Object Storage in an EU region: endpoint, bucket, access key, secret.
5. The image tag to run (`ghcr.io/sunday-studio/shop-api:<x.y.z>`). Public images need no login; the private hosted image needs `docker login` as the deploy user.
6. Your SSH public key.

## Day one

```bash
# 1. On your Mac: copy the kit to the server (root, once), then bootstrap.
scp -r deploy root@SERVER:/root/shop-deploy
ssh root@SERVER
cd /root/shop-deploy/hosted
SSH_PUBKEY="ssh-ed25519 AAAA... you@mac" ./bootstrap.sh      # add DRY_RUN=1 first if you want to read what it does
#    In a SECOND terminal: ssh shop@SERVER  (must work before you close the root session)

# 2. As the deploy user: put the kit where it lives and configure it.
ssh shop@SERVER
cp -r /root/shop-deploy /srv/shop/deploy && cd /srv/shop/deploy/hosted    # or git clone the repo into /srv/shop
cp .env.example .env && chmod 600 .env && nano .env              # domain, ACME_EMAIL, SHOP_API_TAG, Apple ids, alert targets
cp secrets.env.example secrets.env && chmod 600 secrets.env     # sealer key, admin token hash, the two Apple keys (see the file)
./compose.sh run --rm --no-deps api admin token create          # prints an admin token once; put its HASH in secrets.env
./deploy.sh gen-offsite                                          # makes offsite.env.new with fresh crypt keys
nano offsite.env.new && mv offsite.env.new offsite.env           # fill in endpoint, bucket, keys; chmod 600 stays
#    SAVE the crypt password and salt that gen-offsite printed in your password manager.

# 3. Deploy and check.
./deploy.sh preflight
./deploy.sh first
./deploy.sh smoke
```

`first` pulls the images, starts the stack, waits until everything is healthy (OCR needs about two minutes), takes a first backup, copies it off-site, and runs the smoke test. It stops loudly if any of those fail.

Then set up the external uptime monitor (below) and give App Store Connect the notification URL `https://<domain>/v1/billing/apple/notifications` (production and sandbox, version 2).

The kit runs the hosted server, `shop-hosted` (the image holds both binaries; the compose file sets the entrypoint). The home install's `shop` binary, with its pairing password, is not what runs here. Operator commands go through the same binary inside the running container:

```bash
./compose.sh exec api /usr/local/bin/shop-hosted admin grant create --support-code K7PQ-4MXD --reason "Friend" --operator you
./compose.sh exec api /usr/local/bin/shop-hosted admin account find --email someone@example.com
./compose.sh exec api /usr/local/bin/shop-hosted admin lifecycle run --dry-run
```

The HTTP admin API listens on `127.0.0.1:8081` inside the container and is not published; through the front `/admin/...` does not exist (the smoke test checks it).

### Sandbox purchases (App Review and TestFlight)

Apple's reviewers and TestFlight testers buy in Apple's sandbox against this production server. `SHOP_APPLE_ACCEPT_SANDBOX=true` (the default in `.env`) accepts those purchases: they are verified like production ones, stored as `environment=sandbox`, shown as such in `GET /v1/account` (`plan.environment`) and in the admin account lookup, capped at `SHOP_APPLE_SANDBOX_MAX_DAYS` (30) after the first purchase, never counted as revenue (`GET /admin/v1/subscriptions/summary` lists them as `sandbox`, only production subscriptions are `paying`), and ignored when the same owner has a production subscription. **Recommendation: leave it on during beta and App Review; after launch set it to `false` in `.env` and `./compose.sh up -d api` if no TestFlight testing is needed any more** (then sandbox purchases are refused and sandbox notifications ignored). Details: `api/internal/hosted/APPLE.md`.

### What bootstrap.sh does (so you can check it)

Deploy user `shop` with your key and sudo; SSH key-only, no root login, only that user (refuses to harden if no key was given); ufw denies incoming except 22, 80, 443; unattended security upgrades without automatic reboot; 2 GB swap, swappiness 10; Docker Engine and the compose plugin from Docker's official apt repository; fail2ban for SSH (`ENABLE_FAIL2BAN=0` to skip); `/srv/shop`. Docker publishes ports behind ufw's back, which is fine only because the stack publishes nothing but Caddy's 80 and 443: never add `ports:` to another service. It does not reboot: after kernel updates (`/var/run/reboot-required`) reboot in a quiet moment; the stack restarts itself (`restart: unless-stopped`).

## Update to a new image tag

```bash
cd /srv/shop/deploy/hosted
./deploy.sh update 1.3.0
```

It: runs preflight; takes a backup set (control database and every household) and copies it off-site (it refuses to continue if that fails); records the old tag and the snapshot name in `.deploy/`; sets `SHOP_API_TAG`; pulls; restarts changed services; waits until healthy; runs the smoke test. If anything fails it stops and tells you what to run next; it never rolls back by itself, because a database migration may already have happened.

The API also writes its own `pre-migrate-v<N>-<time>.db` into the household folders before a migration.

## Rollback

```bash
./deploy.sh rollback               # previous image tag, same database
./deploy.sh rollback --restore-db  # previous tag AND the pre-update snapshot (use when the new version already migrated the database)
```

`--restore-db` loses everything written since the snapshot (receipts uploaded since the update). Use the plain rollback first if the old version still starts.

## Backups, off-site copy and restore

What is backed up. The hosted data is the control database (`/data/control.db`: accounts, devices, plans, grants, audit) plus one folder per household (`/data/households/<id>/shop.db` and `images/`). The `backup` service writes a **backup set** every night at `BACKUP_AT` into the `backups` volume:

```
db/shop-<ts>.db                    control database snapshot (VACUUM INTO, integrity checked)
households/<id>/db/shop-<ts>.db    snapshot of every household database, same <ts>
households/<id>/images/...         append-only mirror of the household's images
last_backup.json                   written last: the set is complete only when it says ok
```

Each file is a consistent SQLite snapshot taken while the server keeps running; the control database is taken first and the set spans a few minutes at most, so a household created during the run is simply picked up the next night. The `offsite` service then copies the set (households first, then the control snapshots, then `last_backup.json`) to the encrypted rclone remote and writes `offsite_status.json` next to it. `BACKUP_KEEP_DAYS` (30) prunes old snapshots locally; `OFFSITE_PRUNE=on` mirrors that off-site.

**Erased households stay in older backups until they are pruned** (30 days locally, and off-site as long as the remote keeps them). That is a promise to state in the privacy text. A restore never brings an erased household back: after restoring the control database the restore removes the folder of every household that database marks `deleted`.

Restores replace the live data and need `--yes`:

- From the local backup folder (database damaged, server fine): `./deploy.sh restore --yes` (latest control snapshot, newest snapshot of each household) or `--snapshot shop-YYYYMMDD-HHMMSS.db` (a control snapshot; households still take their newest). List snapshots: `./compose.sh run --rm --no-deps --entrypoint ls offsite -1 /backup/db`.
- From the off-site copy (backup volume lost too): `./deploy.sh restore --from-offsite --yes`. It pulls the encrypted copy back (control snapshots and `households/`), decrypts it, restores, starts the stack and runs the smoke test.
- On a NEW server (the old one is gone): do Day one steps 1 and 2 with the SAME `.env`, `secrets.env` (the sealer key: without it stored Apple refresh tokens are unreadable) and `offsite.env` (the crypt password and salt from your password manager), then `./deploy.sh preflight && ./deploy.sh restore --from-offsite --yes`. Change the DNS record to the new IP first. Caddy then fetches a new certificate.
- One household only (it was damaged, nothing else): stop nothing, copy that household's newest `households/<id>/db/shop-*.db` over `/data/households/<id>/shop.db` with the server stopped for that moment; ask before doing this, the general restore is the tested path.

After any restore run `./deploy.sh backup-now` and `./compose.sh exec api /usr/local/bin/shop-hosted admin lifecycle run --dry-run` (the dry run lists what the scheduler would do next; read it before the scheduler's next run). The kit test (`make hosted-kit-test`) rehearses the off-site restore on every run.

## Smoke test

```bash
./deploy.sh smoke                       # https://$SHOP_DOMAIN
./smoke.sh https://shop.example.com     # from any machine
```

Checks that the public `/healthz` says `ok` and shows nothing else, `/v1/server-info` says `mode: hosted`, `/v1/auth/status`, that the API refuses requests without a token, that `/admin/...` is not served on the public address and that `POST /v1/billing/apple/notifications` is up (a bad payload gets 400). The public `/healthz` deliberately shows only the status (no OCR error text, no household counts); the details are for a local caller and for the admin API (`GET /admin/v1/health`).

## Monitoring (free, no vendor needed)

Three layers, use as many as you like:

1. **Inside the stack (`monitor` service, on by default).** Every `MONITOR_INTERVAL` seconds (300) it reads `/healthz`, asks the OCR service for its own `/healthz`, reads `last_backup.json` and `offsite_status.json` from the backups volume (mounted read-only) and checks the disk. It sends a message when something starts failing, repeats it every `ALERT_REPEAT_HOURS` (6) while it lasts, and says when it recovers. Alerts: API not answering 200; OCR not ready; backup failed (the nightly set failed or is older than 36 h, or the encrypted off-site copy failed or is older than 36 h); disk at or above `DISK_ALERT_PCT` (80). The OCR queue alert (`QUEUE_ALERT_*`) needs job counts that the hosted `/healthz` does not show, so it is inactive in this kit. Without any target configured it only writes to its log (`./compose.sh logs monitor`).
2. **Where the message goes** (set in `.env`, any combination):
   - Webhook: `ALERT_WEBHOOK_URL`. `ALERT_WEBHOOK_FORMAT=json` posts `{"text":..,"content":..}`, which Slack, Discord and Mattermost incoming webhooks accept. `=text` posts the plain message (an ntfy.sh topic URL is the zero-signup option: install the ntfy app, subscribe to a long random topic name, use `https://ntfy.sh/<topic>`).
   - Email: `ALERT_EMAIL_TO`, `ALERT_EMAIL_FROM`, `SMTP_URL` (e.g. `smtps://smtp.example.com:465`), `SMTP_USER`, `SMTP_PASSWORD`. Any SMTP account you already have works; curl does the sending.
   Choice: no alerting vendor, no agent, nothing to install on the host; the monitor is one shell script (`monitor/monitor.sh`) you can read. The cost is that it cannot tell you the whole server is dead, which is layer 3.
3. **Outside the server (do this one).** A dead server cannot send its own alert. Either point any uptime service (UptimeRobot, Better Stack and similar free tiers) at `https://<domain>/healthz` with the keyword `"status":"ok"` (HTTP 503 already means degraded), or run `monitor/uptime-check.sh https://<domain> --ping https://hc-ping.com/<uuid>` from cron on another machine, or set `HEALTHCHECKS_PING_URL` so the monitor pings a dead-man's-switch service (healthchecks.io free tier) after every good check and that service alerts you when pings stop. `uptime-check.sh` prints one line and exits 0 (ok) or 2 (critical), the Nagios convention.

Manual check at any time: `./compose.sh run --rm --no-deps monitor once` (exit 0 = all fine).

## Everyday commands

```bash
./deploy.sh status          # services and the last deploy actions (.deploy/history.log)
./deploy.sh logs api        # recent logs (Docker rotates them: 10 MB x 3 per service)
./deploy.sh backup-now      # snapshot + off-site copy now
./compose.sh ps
```

Never run `docker compose` yourself in this folder and never `down -v`: `compose.sh` refuses it.

## Secrets

`.env`, `offsite.env` and `secrets.env` (the sealer key that encrypts stored Apple refresh tokens, the admin token hash, the Sign in with Apple key and the App Store Server API key) are mode 600 and git-ignored; `deploy.sh preflight` checks the mode and that nothing says CHANGE_ME. Keep the sealer key in a password manager too. Keep the rclone crypt password and salt in a password manager as well. To rotate the S3 keys edit `offsite.env` and `./compose.sh up -d offsite`. To change the crypt password you must start a new crypt path (a new bucket folder) and keep the old password for the old copies.

## Troubleshooting

| Symptom | Look at |
|---|---|
| Browser says certificate error | DNS not pointing here yet, or ports 80/443 blocked at UpCloud's firewall as well as ufw; `./compose.sh logs caddy` |
| `/healthz` 503 (only the status is public) | `./compose.sh exec api /usr/local/bin/shop-hosted healthcheck`, `./compose.sh logs api ocr`; the details are in the monitor's alert text |
| The api container restarts at once | `./compose.sh logs api`: a missing Apple id or key in `.env` / `secrets.env` is named in the first line (production refuses to start without them) |
| `/healthz` 503, OCR failing | OCR is still loading (about two minutes after start) or was killed by its memory limit: `./compose.sh logs ocr`, `dmesg \| grep -i oom` |
| Backup alert | `./compose.sh run --rm --no-deps --entrypoint cat offsite /backup/last_backup.json /backup/offsite_status.json`; `./compose.sh logs backup offsite`; wrong S3 keys or a full bucket are the usual causes |
| `deploy.sh first` stops at the backup | the off-site settings are wrong; fix `offsite.env`, `./compose.sh up -d offsite`, `./deploy.sh backup-now` |
| Disk alert | `docker system df` (read only), check `/var/lib/docker/volumes/shop-hosted_*`; images are the growth, see CAPACITY.md |

## What `make hosted-kit-test` covers and does not

Covered, on your Mac with throwaway volumes, running the HOSTED binary with the fake Apple mode (`SHOP_APPLE_MODE=fake` plus its second acknowledgement, only in the test overlay): the compose file (entrypoint `shop-hosted`, read-only root, nothing published but Caddy, admin address on loopback), the wrappers' refusals, bootstrap in a Debian container (twice, idempotent), first deploy, sign in through the front with a fake Apple identity token, a trial purchase for that account (flagged `sandbox`, not counted as revenue), receipt upload and OCR read, the backup set (control database and household snapshot, status files), the encrypted off-site copy, alerts to a webhook, update with snapshot, rollback, restore from the off-site copy (control database and household), teardown. Not covered because it needs the real server, the real internet or real Apple: Let's Encrypt certificate issue, real DNS, UpCloud firewall, ufw and fail2ban actually running (the container cannot), swap and unattended upgrades taking effect, systemd behaviour after a reboot, the real S3 endpoint, OCR speed on UpCloud's CPUs, real Sign in with Apple and App Store Server API calls and notifications (the pinned root certificate against real Apple data), pulling from ghcr. Do these by hand on day one: open `https://<domain>` in a browser, `sudo ufw status`, `sudo fail2ban-client status sshd`, `swapon --show`, `./deploy.sh backup-now` against the real bucket, "Request a Test Notification" in App Store Connect for both environments, and a reboot test.
