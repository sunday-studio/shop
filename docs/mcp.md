# Shop MCP server

Shop exposes its data to an AI assistant over the [Model Context Protocol](https://modelcontextprotocol.io), using the **streamable HTTP** transport. It is served by the same Go binary and port as the REST API.

| | |
|---|---|
| Endpoint | `http://<shop-host>:8080/mcp` (e.g. `http://shop.<tailnet>.ts.net:8080/mcp`) |
| Auth | `Authorization: Bearer <token>`. The same tokens as the REST API |
| Mode | Stateless and JSON responses. No sessions to keep alive, so restarts are harmless |

## 1. Create a token for the assistant

```bash
docker compose -f deploy/docker-compose.yml exec api shop token create assistant
```

The token is printed once. Store it in your assistant's config, and revoke it with `shop token revoke assistant`.

## 2. Point the assistant at it

Most MCP clients accept a JSON config like this:

```json
{
  "mcpServers": {
    "shop": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer st_..." }
    }
  }
}
```

For Claude Code:

```bash
claude mcp add --transport http shop http://localhost:8080/mcp --header "Authorization: Bearer st_..."
```

## Tools

Product and shop arguments take an **id or a name**. Names are fuzzy-matched, and the result shows which product or shop was used. Dates are local `YYYY-MM-DD`. Money is returned as `{cents, eur}` (e.g. `{"cents": 129, "eur": "€1,29"}`).

| Tool | Writes? | What it answers |
|---|---|---|
| `spend_summary(from?, to?, group_by?, shop?)` | | "How much did we spend in September?" "…per shop this year?" `group_by` takes day, week, month, shop or category |
| `list_receipts(from?, to?, shop?, status?, limit?)` | | "What receipts are still waiting for review?" |
| `get_receipt(id)` | | All lines of one receipt, with linked products |
| `search_products(query, limit?)` | | Fuzzy catalog search |
| `price_history(product, shop?)` | | Every purchase, with paid price and price per kg, l or piece |
| `compare_prices(product)` | | "Where is milk cheapest?" Latest, min and max per shop |
| `price_changes(since?, min_pct?)` | | "What got more expensive?" Compares recent purchases with earlier ones |
| `list_shops()` | | |
| `frequent_items(lookback_days?, min_count?)` | | What you buy regularly, and whether it's **due** again |
| `shopping_list_get(include_checked?)` | | The list, with the cheapest known shop per item |
| `shopping_list_add(text, qty?, note?, product?)` | ✓ | Text close to a known product links to it. Re-adding an open product bumps its qty |
| `shopping_list_check(id, checked?)` | ✓ | Tick an item off (or back on) |
| `shopping_list_remove(id)` | ✓ | |

Resources: `shop://shops` and `shop://categories`.

## Example prompts

- "How much did I spend on groceries last month, per shop?"
- "Is oat milk cheaper at Lidl or Albert Heijn?"
- "Which products went up more than 10% since summer?"
- "Make my shopping list: add whatever I'm due to buy again, plus eggs."
- "Show me the receipts that still need review."

## Notes

- Price tools only use receipts with status `ok`, so confirm `needs_review` receipts in the app to include them. Spend includes `needs_review` receipts, since the money was spent either way.
- Price changes compare the median of the latest 3 purchases per product and shop with the 3 before them.
- Smoke test from a shell:

  ```bash
  curl -s localhost:8080/mcp -H "Authorization: Bearer $SHOP_TOKEN" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
  ```
