-- +goose Up
CREATE TABLE shops (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL,
  chain          TEXT,
  branch         TEXT,
  address        TEXT,
  city           TEXT,
  parser_profile TEXT NOT NULL DEFAULT 'generic',
  notes          TEXT,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  deleted_at     TEXT
);

CREATE TABLE categories (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  sort       INTEGER NOT NULL DEFAULT 0,
  deleted_at TEXT
);

CREATE TABLE products (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  brand        TEXT,
  category_id  INTEGER REFERENCES categories(id),
  barcode      TEXT UNIQUE,
  net_quantity REAL,
  net_unit     TEXT CHECK (net_unit IN ('g', 'ml', 'pc')),
  image_path   TEXT,
  off_code     TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  deleted_at   TEXT
);

CREATE TABLE nutrition (
  product_id    INTEGER PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  per           TEXT NOT NULL CHECK (per IN ('100g', '100ml')),
  energy_kcal   REAL,
  fat           REAL,
  saturated_fat REAL,
  carbs         REAL,
  sugars        REAL,
  fibre         REAL,
  protein       REAL,
  salt          REAL,
  source        TEXT NOT NULL CHECK (source IN ('off', 'manual'))
);

CREATE TABLE receipts (
  id               INTEGER PRIMARY KEY,
  shop_id          INTEGER NOT NULL REFERENCES shops(id),
  purchased_at     TEXT,
  total_cents      INTEGER,
  parsed_sum_cents INTEGER,
  currency         TEXT NOT NULL DEFAULT 'EUR',
  payment_method   TEXT,
  image_path       TEXT NOT NULL,
  image_sha256     TEXT NOT NULL UNIQUE,
  status           TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'needs_review', 'ok', 'failed')),
  review_reasons   TEXT,
  ocr_engine       TEXT,
  ocr_ms           INTEGER,
  raw_ocr_json     TEXT,
  parser_version   INTEGER,
  confirmed_at     TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE INDEX receipts_shop_date ON receipts(shop_id, purchased_at);
CREATE INDEX receipts_status ON receipts(status);

CREATE TABLE receipt_lines (
  id               INTEGER PRIMARY KEY,
  receipt_id       INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
  line_no          INTEGER NOT NULL,
  raw_text         TEXT NOT NULL,
  norm_text        TEXT NOT NULL,
  kind             TEXT NOT NULL CHECK (kind IN ('item', 'discount', 'deposit', 'bag', 'other')),
  qty              REAL NOT NULL DEFAULT 1,
  unit             TEXT NOT NULL DEFAULT 'pc' CHECK (unit IN ('pc', 'kg', 'l')),
  unit_price_cents INTEGER,
  line_total_cents INTEGER NOT NULL,
  vat_code         TEXT,
  parent_line_id   INTEGER REFERENCES receipt_lines(id) ON DELETE SET NULL,
  product_id       INTEGER REFERENCES products(id),
  user_edited      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX lines_receipt ON receipt_lines(receipt_id, line_no);
CREATE INDEX lines_product ON receipt_lines(product_id);

CREATE TABLE product_aliases (
  id         INTEGER PRIMARY KEY,
  shop_id    INTEGER REFERENCES shops(id),
  norm_text  TEXT NOT NULL,
  product_id INTEGER NOT NULL REFERENCES products(id),
  created_at TEXT NOT NULL,
  UNIQUE (shop_id, norm_text)
);

-- Fuzzy search over product names and aliases (kind = 'product' | 'alias').
CREATE VIRTUAL TABLE search_fts USING fts5(
  kind UNINDEXED,
  ref_id UNINDEXED,
  text,
  tokenize = 'trigram'
);

CREATE TABLE shopping_list_items (
  id         INTEGER PRIMARY KEY,
  product_id INTEGER REFERENCES products(id),
  text       TEXT NOT NULL,
  qty        REAL,
  note       TEXT,
  checked_at TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE jobs (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  status     TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
  attempts   INTEGER NOT NULL DEFAULT 0,
  run_after  TEXT NOT NULL,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX jobs_ready ON jobs(status, run_after);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  token_hash   TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  last_used_at TEXT
);

-- Item price after its linked discounts. Per-base-unit normalisation happens in Go.
CREATE VIEW price_points AS
SELECT l.id AS line_id,
       l.product_id,
       r.shop_id,
       r.purchased_at,
       l.qty,
       l.unit,
       l.line_total_cents + COALESCE((SELECT SUM(d.line_total_cents)
                                      FROM receipt_lines d
                                      WHERE d.parent_line_id = l.id), 0) AS paid_cents
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
WHERE l.kind = 'item' AND l.product_id IS NOT NULL AND r.status = 'ok';

-- +goose Down
DROP VIEW price_points;
DROP TABLE api_tokens;
DROP TABLE jobs;
DROP TABLE shopping_list_items;
DROP TABLE search_fts;
DROP TABLE product_aliases;
DROP TABLE receipt_lines;
DROP TABLE receipts;
DROP TABLE nutrition;
DROP TABLE products;
DROP TABLE categories;
DROP TABLE shops;
