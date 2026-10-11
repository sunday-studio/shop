---
title: Ask an assistant (MCP)
group: Self-hosting
summary: Let an AI assistant that supports MCP read your spending.
---

## Steps

1. Create a token for the assistant. It is printed once.
   ```bash
   make token NAME=assistant
   ```
2. Add Shop to your assistant. For Claude Code, use your server's address instead of `localhost` if the assistant runs on another machine.
   ```bash
   claude mcp add --transport http shop http://localhost:8080/mcp --header "Authorization: Bearer <token>"
   ```
   Other assistants take the same address and the same `Authorization` header in their configuration.
3. Ask a question.

## Things to ask

- "How much did I spend on groceries last month, per shop?"
- "Which of my shops sells oat milk for less?"
- "Which products went up more than 10% since summer?"
- "Show me the receipts that still need review."

The assistant can read spending, receipts, products, prices, shops and the things you buy regularly. It can also read and change your shopping list.

## Manage tokens

```bash
docker compose -f deploy/docker-compose.yml exec api shop token list
docker compose -f deploy/docker-compose.yml exec api shop token revoke assistant
```

> **Warning:** A token can read all your receipts, and some tools change data: the shopping list, English words and the review of new products. Give a token only to assistants you trust, and keep the server off the internet.

> **Note:** Price tools only use confirmed receipts. Confirm a receipt that needs review to include it.
