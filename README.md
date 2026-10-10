# Shop

Self-hosted grocery receipt tracker: paper receipts → local OCR → SQLite → spend & price comparisons, via an iOS app and an MCP server.

Docs live in [`docs/`](docs/):

- [Build plan](docs/PLAN.md)
- [Operations](docs/operations.md): install, NAS backups, Tailscale, upgrades
- [Restore](docs/restore.md)
- [MCP](docs/mcp.md): connecting your assistant

```bash
make up        # build + start api and ocr containers
curl localhost:8080/healthz
make test      # Go tests
make help      # all targets
```
