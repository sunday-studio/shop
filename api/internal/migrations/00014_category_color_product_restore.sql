-- +goose Up
-- Category colour (one of ten tokens) so renaming a category no longer changes its colour, and the barcode
-- a deleted product had, so "Undo delete" can give it back.
ALTER TABLE categories ADD COLUMN color TEXT
  CHECK (color IN ('blue', 'green', 'red', 'orange', 'yellow', 'cyan', 'purple', 'teal', 'pink', 'grey'));
ALTER TABLE products ADD COLUMN deleted_barcode TEXT;

-- Backfill from the mapping the app used so nothing changes visually.
UPDATE categories SET color = CASE name
  WHEN 'Dairy' THEN 'blue' WHEN 'Produce' THEN 'green' WHEN 'Meat & fish' THEN 'red' WHEN 'Bakery' THEN 'orange'
  WHEN 'Pantry' THEN 'yellow' WHEN 'Frozen' THEN 'cyan' WHEN 'Drinks' THEN 'purple' WHEN 'Household' THEN 'teal'
  WHEN 'Personal care' THEN 'pink' WHEN 'Other' THEN 'grey' END
WHERE color IS NULL;
-- any other category: spread over the tokens by id
UPDATE categories SET color = CASE id % 10
  WHEN 0 THEN 'blue' WHEN 1 THEN 'green' WHEN 2 THEN 'red' WHEN 3 THEN 'orange' WHEN 4 THEN 'yellow'
  WHEN 5 THEN 'cyan' WHEN 6 THEN 'purple' WHEN 7 THEN 'teal' WHEN 8 THEN 'pink' ELSE 'grey' END
WHERE color IS NULL;

-- +goose Down
ALTER TABLE products DROP COLUMN deleted_barcode;
ALTER TABLE categories DROP COLUMN color;
