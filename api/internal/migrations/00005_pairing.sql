-- +goose Up
-- Server-wide settings (e.g. the bcrypt hash of the pairing password).
CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- 'manual' = created with `shop token create`, 'device' = issued by pairing.
ALTER TABLE api_tokens ADD COLUMN kind TEXT NOT NULL DEFAULT 'manual';

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN kind;
DROP TABLE settings;
