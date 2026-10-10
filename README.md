# Shop

Self-hosted grocery receipt tracker: paper receipts → local OCR → SQLite → spend & price comparisons, via an iOS app and an MCP server.

Docs live in [`docs/`](docs/) — start with the [build plan](docs/PLAN.md).

```bash
make up        # build + start api and ocr containers
curl localhost:8080/healthz
make test      # Go tests
make help      # all targets
```
