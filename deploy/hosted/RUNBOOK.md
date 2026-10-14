# Hosted Shop: server runbook

For the paid hosted version on one fresh Linux server (UpCloud, EU, Ubuntu 24.04 LTS or Debian 12 assumed). Everything is in `deploy/hosted/`. The home install (`deploy/docker-compose.yml`) is a different thing; this kit never touches it. Capacity and sizing: [CAPACITY.md](CAPACITY.md).

Scripts: `bootstrap.sh` (once, as root), `deploy.sh` (day-to-day, as the deploy user), `smoke.sh`, `compose.sh` (the only door to docker compose; project `shop-hosted`), `monitor/` (alerts).

## Before day one (owner)

1. A server with 8 GB RAM, 4 vCPU, 80 GB disk or more (see CAPACITY.md; 4 GB is the bare minimum). Note the public IP.
2. A DNS record: `shop.<your domain>` A (and AAAA) pointing at that IP. Caddy cannot get a certificate before this resolves.
3. An S3 bucket for backups, e.g. UpCloud Managed Object Storage in an EU region: endpoint, bucket, access key, secret.
4. The image tag to run (`ghcr.io/sunday-studio/shop-api:<x.y.z>`). Public images need no login; the private hosted image needs `docker login` as the deploy user.
5. Your SSH public key.

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
cp .env.example .env && chmod 600 .env && nano .env              # domain, ACME_EMAIL, SHOP_API_TAG, alert targets
./deploy.sh gen-offsite                                          # makes offsite.env.new with fresh crypt keys
nano offsite.env.new && mv offsite.env.new offsite.env           # fill in endpoint, bucket, keys; chmod 600 stays
#    SAVE the crypt password and salt that gen-offsite printed in your password manager.

# 3. Deploy and check.
./deploy.sh preflight
./deploy.sh first
./deploy.sh smoke
```

`first` pulls the images, starts the stack, waits until everything is healthy (OCR needs about two minutes), takes a first backup, copies it off-site, and runs the smoke test. It stops loudly if any of those fail.

Then set up the external uptime monitor (below). In `single` mode create the pairing password with `./compose.sh exec api shop password generate`. `hosted` mode is implemented by another work package: until the image has it, `SHOP_MODE=hosted` is passed through and ignored.

### What bootstrap.sh does (so you can check it)

Deploy user `shop` with your key and sudo; SSH key-only, no root login, only that user (refuses to harden if no key was given); ufw denies incoming except 22, 80, 443; unattended security upgrades without automatic reboot; 2 GB swap, swappiness 10; Docker Engine and the compose plugin from Docker's official apt repository; fail2ban for SSH (`ENABLE_FAIL2BAN=0` to skip); `/srv/shop`. Docker publishes ports behind ufw's back, which is fine only because the stack publishes nothing but Caddy's 80 and 443: never add `ports:` to another service. It does not reboot: after kernel updates (`/var/run/reboot-required`) reboot in a quiet moment; the stack restarts itself (`restart: unless-stopped`).

## Update to a new image tag

```bash
cd /srv/shop/deploy/hosted
./deploy.sh update 1.3.0
```

It: runs preflight; takes a database snapshot and copies it off-site (it refuses to continue if that fails); records the old tag and the snapshot name in `.deploy/`; sets `SHOP_API_TAG`; pulls; restarts changed services; waits until healthy; runs the smoke test. If anything fails it stops and tells you what to run next; it never rolls back by itself, because a database migration may already have happened.

The API also writes its own `pre-migrate-v<N>-<time>.db` into the data volume before a migration.

## Rollback

```bash
./deploy.sh rollback               # previous image tag, same database
./deploy.sh rollback --restore-db  # previous tag AND the pre-update snapshot (use when the new version already migrated the database)
```

`--restore-db` loses everything written since the snapshot (receipts uploaded since the update). Use the plain rollback first if the old version still starts.

## Restore

Restores replace the live database and need `--yes`.

- From the local backup folder (database damaged, server fine): `./deploy.sh restore --yes` (latest) or `--snapshot shop-YYYYMMDD-HHMMSS.db`. List snapshots: `./compose.sh run --rm --no-deps --entrypoint ls offsite -1 /backup/db`.
- From the off-site copy (backup volume lost too): `./deploy.sh restore --from-offsite --yes`. It pulls the encrypted copy back, decrypts it, restores the newest snapshot and its images, starts the stack and runs the smoke test.
- On a NEW server (the old one is gone): do Day one steps 1 and 2 with the SAME `.env` and the SAME `offsite.env` (the crypt password and salt from your password manager; without them the copy is unreadable), then `./deploy.sh preflight && ./deploy.sh restore --from-offsite --yes`. Change the DNS record to the new IP first. Caddy then fetches a new certificate.

After a restore `/healthz` has no off-site status until the next backup: run `./deploy.sh backup-now`. The kit test (`make hosted-kit-test`) rehearses this restore on every run.

## Smoke test

```bash
./deploy.sh smoke                       # https://$SHOP_DOMAIN
./smoke.sh https://shop.example.com     # from any machine
```

Checks `/healthz` (db and ocr ok), `/v1/server-info` (SKIPPED while the image does not have it yet; set `REQUIRE_SERVER_INFO=1` once it does), `/v1/auth/status`, and that the API refuses requests without a token.

## Monitoring (free, no vendor needed)

Three layers, use as many as you like:

1. **Inside the stack (`monitor` service, on by default).** Every `MONITOR_INTERVAL` seconds (300) it reads `/healthz` and the disk, and sends a message when something starts failing, repeats it every `ALERT_REPEAT_HOURS` (6) while it lasts, and says when it recovers. Alerts: API down or db failing; OCR not ready; backup failed (the nightly snapshot, no good backup for 36 h, or the encrypted off-site copy; the API folds the off-site result into `/healthz`); disk at or above `DISK_ALERT_PCT` (80); OCR queue backlog (more than `QUEUE_ALERT_PENDING` jobs for `QUEUE_ALERT_CHECKS` checks in a row). Without any target configured it only writes to its log (`./compose.sh logs monitor`).
2. **Where the message goes** (set in `.env`, any combination):
   - Webhook: `ALERT_WEBHOOK_URL`. `ALERT_WEBHOOK_FORMAT=json` posts `{"text":..,"content":..}`, which Slack, Discord and Mattermost incoming webhooks accept. `=text` posts the plain message (an ntfy.sh topic URL is the zero-signup option: install the ntfy app, subscribe to a long random topic name, use `https://ntfy.sh/<topic>`).
   - Email: `ALERT_EMAIL_TO`, `ALERT_EMAIL_FROM`, `SMTP_URL` (e.g. `smtps://smtp.example.com:465`), `SMTP_USER`, `SMTP_PASSWORD`. Any SMTP account you already have works; curl does the sending.
   Choice: no alerting vendor, no agent, nothing to install on the host; the monitor is one 120-line shell script (`monitor/monitor.sh`) you can read. The cost is that it cannot tell you the whole server is dead, which is layer 3.
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

`.env`, `offsite.env` and the optional `secrets.env` (for the hosted layer: Sign in with Apple key, App Store Server API key, token encryption key) are mode 600 and git-ignored; `deploy.sh preflight` checks the mode. Keep the rclone crypt password and salt in a password manager as well. To rotate the S3 keys edit `offsite.env` and `./compose.sh up -d offsite`. To change the crypt password you must start a new crypt path (a new bucket folder) and keep the old password for the old copies.

## Troubleshooting

| Symptom | Look at |
|---|---|
| Browser says certificate error | DNS not pointing here yet, or ports 80/443 blocked at UpCloud's firewall as well as ufw; `./compose.sh logs caddy` |
| `/healthz` 503, `ocr` failing | OCR is still loading (about two minutes after start) or was killed by its memory limit: `./compose.sh logs ocr`, `dmesg \| grep -i oom` |
| `/healthz` 503, `backup` failing | read the detail in the JSON; `./compose.sh logs backup offsite`; wrong S3 keys or a full bucket are the usual causes |
| `deploy.sh first` stops at the backup | the off-site settings are wrong; fix `offsite.env`, `./compose.sh up -d offsite`, `./deploy.sh backup-now` |
| Disk alert | `docker system df` (read only), check `/var/lib/docker/volumes/shop-hosted_*`; images are the growth, see CAPACITY.md |

## What `make hosted-kit-test` covers and does not

Covered, on your Mac with throwaway volumes: the compose file, the wrappers' refusals, bootstrap in a Debian container (twice, idempotent), first deploy, upload and read in single mode, encrypted off-site copy, alerts to a webhook, update with snapshot, rollback, restore from the off-site copy, teardown. Not covered because it needs the real server or the real internet: Let's Encrypt certificate issue, real DNS, UpCloud firewall, ufw and fail2ban actually running (the container cannot), swap and unattended upgrades taking effect, systemd behaviour after a reboot, the real S3 endpoint, OCR speed on UpCloud's CPUs, `SHOP_MODE=hosted` behaviour, pulling from ghcr. Do these by hand on day one: open `https://<domain>` in a browser, `sudo ufw status`, `sudo fail2ban-client status sshd`, `swapon --show`, `./deploy.sh backup-now` against the real bucket, and a reboot test.
