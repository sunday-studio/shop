---
title: Install the server
group: Getting started
summary: Start the Docker stack and get the pairing password.
---

## You need

{lettered}
- Docker, with about 4 GB of memory to spare
- git and make
- A Mac mini is what it was built on

## Steps

1. Get the code.
   ```bash
   git clone https://github.com/sunday-studio/shop shop && cd shop
   ```
2. Optional: copy the settings file. Add a logo.dev key if you want real shop logos; without it the app shows initials.
   ```bash
   cp deploy/.env.example deploy/.env
   ```
3. Build and start.
   ```bash
   make setup
   ```
   The first build downloads the OCR models and takes several minutes. When it ends it prints the pairing password once. Copy it.
   ```terminal
   $ make setup
   …
   pairing password (shown once; enter it in the app with the server URL):
   maple-orbit-cedar-tide-42
   ```
4. Check that everything is healthy.
   ```bash
   curl localhost:8080/healthz
   ```
   Every check should say `ok`.

> **Warning:** The server speaks plain HTTP. Do not expose it to the internet. Reach it over your home network or a VPN (see the next page).

## Settings

Set these in `deploy/.env`.

| Setting | Default | Meaning |
|---|---|---|
| `SHOP_PORT` | `8080` | Port for the API and MCP |
| `SHOP_BACKUP_DIR` | `backups/` | Where nightly backups go (point it at a NAS) |
| `BACKUP_AT` | `03:30` | Daily backup time |
| `BACKUP_KEEP_DAYS` | `30` | How long snapshots are kept |
| `LOGO_DEV_TOKEN` | none | Shop logos |
| `AUTO_PRODUCTS` | `on` | Create products when you confirm a receipt |
