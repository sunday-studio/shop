-- +goose Up
-- Phase A of the shopping list: confirming a receipt checks off what was planned.

-- How an item came to be checked. 'receipt' items point at the receipt and line that did it.
ALTER TABLE shopping_list_items ADD COLUMN check_source TEXT NOT NULL DEFAULT 'manual'
  CHECK (check_source IN ('manual', 'receipt'));
ALTER TABLE shopping_list_items ADD COLUMN checked_receipt_id INTEGER;
ALTER TABLE shopping_list_items ADD COLUMN checked_line_id INTEGER;

-- "Looks bought?": a receipt line that probably matches an open item, but not surely enough to check it.
CREATE TABLE shopping_list_suggestions (
  id          INTEGER PRIMARY KEY,
  item_id     INTEGER NOT NULL REFERENCES shopping_list_items(id) ON DELETE CASCADE,
  receipt_id  INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
  line_id     INTEGER,
  score       REAL NOT NULL,
  reason      TEXT NOT NULL CHECK (reason IN ('match', 'substitute')),
  state       TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'accepted', 'dismissed')),
  created_at  TEXT NOT NULL,
  resolved_at TEXT,
  UNIQUE (item_id, receipt_id)
);
CREATE INDEX list_suggestions_open ON shopping_list_suggestions(state) WHERE state = 'open';

-- What a receipt did to each item, so undo restores it exactly and a repeated confirm changes nothing.
-- A row stays after an undo (undone_at set) so the same receipt never re-checks the item.
CREATE TABLE shopping_list_receipt_checks (
  id          INTEGER PRIMARY KEY,
  receipt_id  INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
  item_id     INTEGER NOT NULL REFERENCES shopping_list_items(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('checked', 'reduced')),
  line_id     INTEGER,
  bought_qty  REAL,
  prev_qty    REAL,
  prev_note   TEXT,
  set_note    TEXT,
  created_at  TEXT NOT NULL,
  undone_at   TEXT,
  UNIQUE (receipt_id, item_id)
);

-- +goose Down
DROP TABLE shopping_list_receipt_checks;
DROP INDEX list_suggestions_open;
DROP TABLE shopping_list_suggestions;
ALTER TABLE shopping_list_items DROP COLUMN checked_line_id;
ALTER TABLE shopping_list_items DROP COLUMN checked_receipt_id;
ALTER TABLE shopping_list_items DROP COLUMN check_source;
