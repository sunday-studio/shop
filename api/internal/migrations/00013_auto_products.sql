-- +goose Up
-- Automatic product creation (docs/PRODUCT-AUTOPOPULATE.md §5).
ALTER TABLE products ADD COLUMN created_via TEXT NOT NULL DEFAULT 'manual'
  CHECK (created_via IN ('manual', 'receipt', 'backfill', 'barcode'));
ALTER TABLE products ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN review_reason TEXT
  CHECK (review_reason IN ('near_match', 'barcode_name_match', 'odd_name', 'no_category',
                           'barcode_minimal', 'barcode_off_unavailable'));
ALTER TABLE products ADD COLUMN suggested_merge_id INTEGER REFERENCES products(id);
ALTER TABLE products ADD COLUMN merged_into_id INTEGER REFERENCES products(id);
ALTER TABLE products ADD COLUMN category_source TEXT
  CHECK (category_source IN ('rule', 'off', 'manual'));
CREATE INDEX products_review ON products(needs_review) WHERE needs_review = 1;

CREATE TABLE product_merges (
  id                INTEGER PRIMARY KEY,
  from_product_id   INTEGER NOT NULL REFERENCES products(id),
  into_product_id   INTEGER REFERENCES products(id),       -- NULL for a discard
  line_ids          TEXT NOT NULL,                         -- JSON: receipt_lines that were moved / unlinked
  alias_ids         TEXT NOT NULL,                         -- JSON: product_aliases that were moved / removed
  alias_rows        TEXT NOT NULL DEFAULT '[]',            -- JSON: removed aliases (discard), to re-create
  into_fields_before TEXT NOT NULL DEFAULT '{}',           -- JSON: fields of `into` that were empty and got filled
  from_review_before TEXT NOT NULL DEFAULT '{}',         -- JSON: from-product needs_review, review_reason, suggested_merge_id, barcode before
  kind              TEXT NOT NULL DEFAULT 'merge' CHECK (kind IN ('merge', 'discard', 'split')),
  created_at        TEXT NOT NULL,
  undone_at         TEXT
);

CREATE TABLE product_distinct (
  a_id       INTEGER NOT NULL REFERENCES products(id),
  b_id       INTEGER NOT NULL REFERENCES products(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY (a_id, b_id),
  CHECK (a_id < b_id)
);

-- User-taught category keywords; the built-in seed is embedded in the binary.
CREATE TABLE category_keywords (
  keyword     TEXT PRIMARY KEY,
  category_id INTEGER NOT NULL REFERENCES categories(id),
  updated_at  TEXT NOT NULL
);

-- +goose Down
DROP TABLE category_keywords;
DROP TABLE product_distinct;
DROP TABLE product_merges;
DROP INDEX products_review;
ALTER TABLE products DROP COLUMN category_source;
ALTER TABLE products DROP COLUMN merged_into_id;
ALTER TABLE products DROP COLUMN suggested_merge_id;
ALTER TABLE products DROP COLUMN review_reason;
ALTER TABLE products DROP COLUMN needs_review;
ALTER TABLE products DROP COLUMN created_via;
