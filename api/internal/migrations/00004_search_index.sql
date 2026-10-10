-- +goose Up
-- Rebuild the search index with diacritics folding and keep it in sync
-- with triggers. Rows: kind='product' (name + brand) and kind='alias'
-- (normalised receipt text); ref_id is always the product id.
DROP TABLE search_fts;
CREATE VIRTUAL TABLE search_fts USING fts5(
  kind UNINDEXED,
  ref_id UNINDEXED,
  shop_id UNINDEXED,
  text,
  tokenize = 'trigram remove_diacritics 1'
);

INSERT INTO search_fts (kind, ref_id, shop_id, text)
SELECT 'product', id, NULL, lower(name || ' ' || COALESCE(brand, ''))
FROM products WHERE deleted_at IS NULL;

INSERT INTO search_fts (kind, ref_id, shop_id, text)
SELECT 'alias', product_id, shop_id, norm_text FROM product_aliases;

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

-- +goose StatementBegin
CREATE TRIGGER aliases_fts_ins AFTER INSERT ON product_aliases BEGIN
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  VALUES ('alias', NEW.product_id, NEW.shop_id, NEW.norm_text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER aliases_fts_del AFTER DELETE ON product_aliases BEGIN
  DELETE FROM search_fts
  WHERE kind = 'alias' AND ref_id = OLD.product_id AND text = OLD.norm_text
    AND shop_id IS OLD.shop_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER aliases_fts_upd AFTER UPDATE ON product_aliases BEGIN
  DELETE FROM search_fts
  WHERE kind = 'alias' AND ref_id = OLD.product_id AND text = OLD.norm_text
    AND shop_id IS OLD.shop_id;
  INSERT INTO search_fts (kind, ref_id, shop_id, text)
  VALUES ('alias', NEW.product_id, NEW.shop_id, NEW.norm_text);
END;
-- +goose StatementEnd

CREATE INDEX aliases_product ON product_aliases(product_id);
CREATE INDEX products_name ON products(name);

-- +goose Down
DROP INDEX products_name;
DROP INDEX aliases_product;
DROP TRIGGER aliases_fts_upd;
DROP TRIGGER aliases_fts_del;
DROP TRIGGER aliases_fts_ins;
DROP TRIGGER products_fts_upd;
DROP TRIGGER products_fts_ins;
DROP TABLE search_fts;
CREATE VIRTUAL TABLE search_fts USING fts5(kind UNINDEXED, ref_id UNINDEXED, text, tokenize = 'trigram');
