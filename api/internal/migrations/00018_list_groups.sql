-- +goose Up
-- Phase B of the shopping list: groups (checklists and templates) inside the one list, plus the sync
-- groundwork: a global rev counter, tombstones keyed by kind and id, and kind-aware client ids.

CREATE TABLE shopping_list_groups (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT NOT NULL,
  kind         TEXT NOT NULL DEFAULT 'checklist' CHECK (kind IN ('checklist', 'template')),
  note         TEXT,
  source       TEXT NOT NULL DEFAULT 'user' CHECK (source IN ('user', 'assistant')),
  -- lets an assistant create a group idempotently ("bbq-2026-10-12"); unique per source
  external_ref TEXT,
  sort         INTEGER NOT NULL DEFAULT 0,
  archived_at  TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  rev          INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX list_groups_external ON shopping_list_groups(source, external_ref) WHERE external_ref IS NOT NULL;
CREATE INDEX list_groups_rev ON shopping_list_groups(rev);

-- group_id has no foreign key on purpose: the service moves or deletes items before it deletes a group,
-- and a restore can put them back.
ALTER TABLE shopping_list_items ADD COLUMN group_id INTEGER;
ALTER TABLE shopping_list_items ADD COLUMN source TEXT NOT NULL DEFAULT 'user' CHECK (source IN ('user', 'assistant'));
ALTER TABLE shopping_list_items ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
CREATE INDEX list_items_group ON shopping_list_items(group_id);
CREATE INDEX list_items_rev ON shopping_list_items(rev);

-- One counter for every list write. A client remembers the rev it has seen and asks for everything above it.
-- pruned_rev is the highest rev of any tombstone that was forgotten: a client older than that must resync.
CREATE TABLE shopping_list_rev (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  rev        INTEGER NOT NULL,
  pruned_rev INTEGER NOT NULL DEFAULT 0
);

-- Tombstones for items and groups (replaces shopping_list_deletions, which only knew item ids).
CREATE TABLE shopping_list_tombstones (
  kind       TEXT NOT NULL CHECK (kind IN ('item', 'group')),
  id         INTEGER NOT NULL,
  deleted_at TEXT NOT NULL,
  rev        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (kind, id)
);
CREATE INDEX list_tombstones_rev ON shopping_list_tombstones(rev);

-- Client ids now say what they made: an item or a group.
ALTER TABLE shopping_list_client_ids ADD COLUMN kind TEXT NOT NULL DEFAULT 'item' CHECK (kind IN ('item', 'group'));

-- A deleted group is kept here for a while so "Undo" can put it and its items back.
CREATE TABLE shopping_list_group_trash (
  group_id    INTEGER PRIMARY KEY,
  mode        TEXT NOT NULL CHECK (mode IN ('keep', 'delete')),
  snapshot    TEXT NOT NULL,
  deleted_at  TEXT NOT NULL,
  restored_at TEXT
);

-- Existing rows get revs in the order they were last touched; old tombstones come after them.
INSERT INTO shopping_list_rev (id, rev) VALUES (1, 0);
UPDATE shopping_list_items SET rev = (
  SELECT r FROM (
    SELECT id AS rid, ROW_NUMBER() OVER (ORDER BY COALESCE(updated_at, created_at), id) AS r FROM shopping_list_items
  ) WHERE rid = shopping_list_items.id
);
UPDATE shopping_list_rev SET rev = (SELECT COUNT(*) FROM shopping_list_items);
INSERT INTO shopping_list_tombstones (kind, id, deleted_at, rev)
SELECT 'item', item_id, deleted_at,
       (SELECT rev FROM shopping_list_rev) + ROW_NUMBER() OVER (ORDER BY deleted_at, item_id)
FROM shopping_list_deletions;
UPDATE shopping_list_rev SET rev = rev + (SELECT COUNT(*) FROM shopping_list_deletions);
DROP TABLE shopping_list_deletions;

-- Every insert or update stamps the row with the next rev. An UPDATE that already sets a new rev (the
-- trigger's own) does not fire the trigger again. A new row with a reused id clears the old tombstone.
-- +goose StatementBegin
CREATE TRIGGER list_items_rev_ins AFTER INSERT ON shopping_list_items BEGIN
  UPDATE shopping_list_rev SET rev = rev + 1 WHERE id = 1;
  UPDATE shopping_list_items SET rev = (SELECT rev FROM shopping_list_rev WHERE id = 1) WHERE id = NEW.id;
  DELETE FROM shopping_list_tombstones WHERE kind = 'item' AND id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER list_items_rev_upd AFTER UPDATE ON shopping_list_items WHEN NEW.rev IS OLD.rev BEGIN
  UPDATE shopping_list_rev SET rev = rev + 1 WHERE id = 1;
  UPDATE shopping_list_items SET rev = (SELECT rev FROM shopping_list_rev WHERE id = 1) WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER list_groups_rev_ins AFTER INSERT ON shopping_list_groups BEGIN
  UPDATE shopping_list_rev SET rev = rev + 1 WHERE id = 1;
  UPDATE shopping_list_groups SET rev = (SELECT rev FROM shopping_list_rev WHERE id = 1) WHERE id = NEW.id;
  DELETE FROM shopping_list_tombstones WHERE kind = 'group' AND id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER list_groups_rev_upd AFTER UPDATE ON shopping_list_groups WHEN NEW.rev IS OLD.rev BEGIN
  UPDATE shopping_list_rev SET rev = rev + 1 WHERE id = 1;
  UPDATE shopping_list_groups SET rev = (SELECT rev FROM shopping_list_rev WHERE id = 1) WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER list_tombstones_rev_ins AFTER INSERT ON shopping_list_tombstones BEGIN
  UPDATE shopping_list_rev SET rev = rev + 1 WHERE id = 1;
  UPDATE shopping_list_tombstones SET rev = (SELECT rev FROM shopping_list_rev WHERE id = 1) WHERE kind = NEW.kind AND id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER list_tombstones_rev_upd AFTER UPDATE ON shopping_list_tombstones WHEN NEW.rev IS OLD.rev BEGIN
  UPDATE shopping_list_rev SET rev = rev + 1 WHERE id = 1;
  UPDATE shopping_list_tombstones SET rev = (SELECT rev FROM shopping_list_rev WHERE id = 1) WHERE kind = NEW.kind AND id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER list_tombstones_rev_upd;
DROP TRIGGER list_tombstones_rev_ins;
DROP TRIGGER list_groups_rev_upd;
DROP TRIGGER list_groups_rev_ins;
DROP TRIGGER list_items_rev_upd;
DROP TRIGGER list_items_rev_ins;
CREATE TABLE shopping_list_deletions (
  item_id    INTEGER PRIMARY KEY,
  deleted_at TEXT NOT NULL
);
INSERT INTO shopping_list_deletions (item_id, deleted_at) SELECT id, deleted_at FROM shopping_list_tombstones WHERE kind = 'item';
DROP TABLE shopping_list_group_trash;
ALTER TABLE shopping_list_client_ids DROP COLUMN kind;
DROP INDEX list_tombstones_rev;
DROP TABLE shopping_list_tombstones;
DROP TABLE shopping_list_rev;
DROP INDEX list_items_rev;
DROP INDEX list_items_group;
ALTER TABLE shopping_list_items DROP COLUMN rev;
ALTER TABLE shopping_list_items DROP COLUMN source;
ALTER TABLE shopping_list_items DROP COLUMN group_id;
DROP INDEX list_groups_rev;
DROP INDEX list_groups_external;
DROP TABLE shopping_list_groups;
