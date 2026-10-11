# Shop

A self-hosted grocery receipt tracker. Photograph a paper receipt, and Shop reads it with local OCR, stores it in SQLite, and answers the questions you actually have: what did I spend this month, which shop is cheapest for milk, what got more expensive.

Everything runs on your own machine (CPU only, in Docker). Nothing is sent to a cloud service except two optional lookups: [Open Food Facts](https://world.openfoodfacts.org) for barcode scans and [logo.dev](https://logo.dev) for shop logos.

## What it does

- **Receipt pipeline:** upload a photo → OCR → a deterministic parser → validation against the printed total. Receipts that don't add up are flagged for review instead of guessed.
- **Products:** lines are matched to products with fuzzy matching and learned per-shop aliases. New products are created automatically when you confirm a receipt; anything uncertain waits in a review queue, and every merge can be undone for 30 days.
- **English names:** Dutch product names get an English name from a built-in dictionary you can teach.
- **Shops:** the shop is suggested from the receipt header (chain + branch), with logos.
- **Stats:** spend by time, shop, category and product; price per kg/l/piece; price changes; items you are due to buy again.
- **Shopping list** with offline-safe sync.
- **Barcodes:** scan a product to create it from Open Food Facts.
- **MCP server:** ask an AI assistant about your spending ([docs/mcp.md](docs/mcp.md)).
- **Backups:** nightly snapshots of the database and images to a folder or NAS, with restore ([docs/restore.md](docs/restore.md)).

It is built for **Dutch receipts** (Albert Heijn, Dirk and similar): the parser, glossary and category rules are Dutch, and the default timezone is Europe/Amsterdam. Other languages need their own parser rules and glossary.

## Parts

| Path | What |
|---|---|
| `api/` | The Go server: REST API (`/v1`), MCP (`/mcp`), job queue, SQLite migrations, CLI (`shop`) |
| `ocr/` | OCR sidecar (Python, PaddleOCR PP-OCRv5) behind a small HTTP contract |
| `deploy/` | Docker Compose stack (api, ocr, nightly backup) |
| `ios/` | The iPhone app (SwiftUI), a client of the API |
| `openapi.yaml` | The API contract (the source of truth; the Go types are generated from it) |
| `design/` | Design rules, motion, logo |
| `spike/` | Tools and notes from the OCR evaluation (receipts and OCR output stay local) |
| `docs/` | Plan, operations, restore, MCP, iOS notes |

## Quick start

Requires Docker with about 4 GB of memory to spare (the OCR container is limited to 3 GB).

```bash
cp deploy/.env.example deploy/.env     # optional: add a logo.dev key
make setup                             # build, start, and print the app pairing password once
curl localhost:8080/healthz            # every dependency, no auth
```

The first build downloads the OCR dependencies and models and takes several minutes. Then pair the iOS app with the server URL and the printed password, or use the API directly with a token:

```bash
make token NAME=me                     # a bearer token for scripts and MCP
```

Other targets: `make help`. Configuration (ports, backups, logos, `AUTO_PRODUCTS`, OCR engines) is in [docs/operations.md](docs/operations.md).

The server speaks plain HTTP. Do not expose it to the internet; reach it over your LAN or a VPN such as Tailscale (see the operations guide).

## Development

```bash
make test        # Go tests (api/)
make vet         # go vet + gofmt check
make gen         # regenerate sqlc queries and the OpenAPI Go types after editing openapi.yaml or api/internal/store/queries
make run         # run the API locally against ./data (needs an OCR service at localhost:8000)
```

Conventions that matter: money is integer cents; times are stored in UTC; `openapi.yaml` is edited first and the code is generated from it; migrations are append-only. More in [docs/BACKEND-STATUS.md](docs/BACKEND-STATUS.md).

The iOS app is opened from `ios/Shop/Shop.xcodeproj` (Xcode 27, iOS 17+); see [docs/IOS-SETUP.md](docs/IOS-SETUP.md). It has a `-mock` launch argument that runs against an in-memory server.

## Docs

- [Operations](docs/operations.md): install, configuration, NAS backups, Tailscale, upgrades
- [Restore](docs/restore.md)
- [MCP](docs/mcp.md): connecting your assistant
- [Backend status](docs/BACKEND-STATUS.md): what is built, open items, conventions
- [Build plan](docs/PLAN.md): the spec and milestone checklists
- [Product auto-population](docs/PRODUCT-AUTOPOPULATE.md): how products are created and reviewed
- [OCR spike](docs/spike.md): how OCR engines and the parser were evaluated
- iOS: [setup](docs/IOS-SETUP.md), [plan](docs/IOS-PLAN.md), [backend needs](docs/IOS-BACKEND-NEEDS.md); design: [system](docs/DESIGN-SYSTEM.md)

## License

[MIT](LICENSE)
