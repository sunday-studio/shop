-- +goose Up
-- Phase C of the shopping list: "same kind of item" groups. House brands never repeat across shops, so the
-- cheapest-store plan compares groups of products (e.g. the milk of two shops) that the user confirmed once.
-- Nothing is grouped automatically: suggestions are computed on demand and only a confirm writes here.

CREATE TABLE product_groups (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  category_id INTEGER REFERENCES categories(id),
  -- kg | l | pc: what the members are priced in, taken from the first member with a pack size
  base_unit   TEXT CHECK (base_unit IN ('kg', 'l', 'pc')),
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

-- No foreign key on purpose: the service moves products out before it deletes a group, and an undo puts
-- groups back with their old ids.
ALTER TABLE products ADD COLUMN group_id INTEGER;
CREATE INDEX products_group ON products(group_id) WHERE group_id IS NOT NULL;

-- Pairs the user said are NOT the same kind of item; they are never suggested again.
CREATE TABLE product_group_denied (
  a_id       INTEGER NOT NULL REFERENCES products(id),
  b_id       INTEGER NOT NULL REFERENCES products(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY (a_id, b_id),
  CHECK (a_id < b_id)
);

-- One row per change to groups, with what is needed to reverse it exactly (JSON snapshot).
CREATE TABLE product_group_ops (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL CHECK (kind IN ('confirm', 'merge', 'split', 'delete', 'deny')),
  snapshot   TEXT NOT NULL,
  created_at TEXT NOT NULL,
  undone_at  TEXT
);

-- +goose Down
DROP TABLE product_group_ops;
DROP TABLE product_group_denied;
DROP INDEX products_group;
ALTER TABLE products DROP COLUMN group_id;
DROP TABLE product_groups;
