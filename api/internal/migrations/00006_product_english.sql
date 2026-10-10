-- +goose Up
-- English product name next to the (usually Dutch) name. Source records
-- where it came from: 'manual' (typed by the user), 'openfoodfacts', or
-- 'dictionary' (internal/translate). Dictionary names are regenerated when
-- the Dutch name changes; manual and Open Food Facts names are kept.
ALTER TABLE products ADD COLUMN name_en TEXT;
ALTER TABLE products ADD COLUMN name_en_source TEXT
  CHECK (name_en_source IN ('manual', 'openfoodfacts', 'dictionary'));

-- Index English names as separate rows (kind = 'product_en') so a match on
-- either language scores on its own, instead of diluting one long string.
DROP TRIGGER products_fts_ins;
DROP TRIGGER products_fts_upd;

-- +goose StatementBegin
CREATE TRIGGER products_fts_ins AFTER INSERT ON products WHEN NEW.deleted_at IS NULL BEGIN
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  VALUES ('product', NEW.id, NULL, lower(NEW.name || ' ' || COALESCE(NEW.brand, '')));
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  SELECT 'product_en', NEW.id, NULL, lower(NEW.name_en) WHERE NEW.name_en IS NOT NULL AND NEW.name_en != '';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER products_fts_upd AFTER UPDATE OF name, brand, name_en, deleted_at ON products BEGIN
  DELETE FROM search_fts WHERE kind IN ('product', 'product_en') AND ref_id = OLD.id;
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  SELECT 'product', NEW.id, NULL, lower(NEW.name || ' ' || COALESCE(NEW.brand, ''))
  WHERE NEW.deleted_at IS NULL;
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  SELECT 'product_en', NEW.id, NULL, lower(NEW.name_en)
  WHERE NEW.deleted_at IS NULL AND NEW.name_en IS NOT NULL AND NEW.name_en != '';
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER products_fts_upd;
DROP TRIGGER products_fts_ins;
DELETE FROM search_fts WHERE kind = 'product_en';
-- +goose StatementBegin
CREATE TRIGGER products_fts_ins AFTER INSERT ON products WHEN NEW.deleted_at IS NULL BEGIN
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  VALUES ('product', NEW.id, NULL, lower(NEW.name || ' ' || COALESCE(NEW.brand, '')));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER products_fts_upd AFTER UPDATE OF name, brand, deleted_at ON products BEGIN
  DELETE FROM search_fts WHERE kind = 'product' AND ref_id = OLD.id;
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  SELECT 'product', NEW.id, NULL, lower(NEW.name || ' ' || COALESCE(NEW.brand, ''))
  WHERE NEW.deleted_at IS NULL;
END;
-- +goose StatementEnd
ALTER TABLE products DROP COLUMN name_en_source;
ALTER TABLE products DROP COLUMN name_en;
