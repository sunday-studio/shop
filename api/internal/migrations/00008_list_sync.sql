-- +goose Up
-- Offline-friendly shopping list (iOS backend needs #5, #6).

-- Every write bumps updated_at so clients can sync changes since a time.
ALTER TABLE shopping_list_items ADD COLUMN updated_at TEXT;
UPDATE shopping_list_items SET updated_at = COALESCE(checked_at, created_at);
CREATE INDEX list_updated ON shopping_list_items(updated_at);

-- Tombstones so deletions sync too (pruned after 30 days).
CREATE TABLE shopping_list_deletions (
  item_id    INTEGER PRIMARY KEY,
  deleted_at TEXT NOT NULL
);

-- Client-generated ids make retried offline adds idempotent: a repeat
-- returns the item the first request created or merged into.
CREATE TABLE shopping_list_client_ids (
  client_id  TEXT PRIMARY KEY,
  item_id    INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE shopping_list_client_ids;
DROP TABLE shopping_list_deletions;
DROP INDEX list_updated;
ALTER TABLE shopping_list_items DROP COLUMN updated_at;
