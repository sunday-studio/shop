-- +goose Up
-- A receipt may be uploaded before anyone knows its shop ("Unassigned"), and may carry the time the
-- photo was taken as a hint for the purchase date (never used as the purchase date by itself).
--
-- receipts.shop_id loses NOT NULL. Rebuilding the table would cascade-delete receipt_lines (foreign
-- keys are on), so relax the constraint in place, which SQLite documents as safe for a change that
-- only removes a NOT NULL: edit the stored CREATE TABLE text.
ALTER TABLE receipts ADD COLUMN photo_taken_at TEXT;

-- (ADD COLUMN must come first: it locates the end of the stored CREATE TABLE text by an offset
-- that the edit below would invalidate.)
PRAGMA writable_schema = ON;
UPDATE sqlite_schema
SET sql = replace(sql, 'shop_id          INTEGER NOT NULL REFERENCES shops(id)', 'shop_id          INTEGER REFERENCES shops(id)')
WHERE type = 'table' AND name = 'receipts';
PRAGMA writable_schema = OFF;
-- Bump the schema cookie so other open connections re-read the table definition.
CREATE TABLE _schema_bump (x INTEGER);
DROP TABLE _schema_bump;

-- +goose Down
ALTER TABLE receipts DROP COLUMN photo_taken_at;
UPDATE receipts SET shop_id = (SELECT MIN(id) FROM shops) WHERE shop_id IS NULL;
PRAGMA writable_schema = ON;
UPDATE sqlite_schema
SET sql = replace(sql, 'shop_id          INTEGER REFERENCES shops(id)', 'shop_id          INTEGER NOT NULL REFERENCES shops(id)')
WHERE type = 'table' AND name = 'receipts';
PRAGMA writable_schema = OFF;
CREATE TABLE _schema_bump (x INTEGER);
DROP TABLE _schema_bump;
