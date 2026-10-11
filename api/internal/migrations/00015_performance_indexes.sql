-- +goose Up
-- The price_points view sums each line's discounts with a correlated subquery on parent_line_id; without an
-- index that is a full scan of receipt_lines per line (minutes on a few thousand receipts).
CREATE INDEX lines_parent ON receipt_lines(parent_line_id) WHERE parent_line_id IS NOT NULL;
-- relinking and splitting look lines up by their normalised text
CREATE INDEX lines_norm ON receipt_lines(norm_text) WHERE kind = 'item';
-- category counts, deleting a category, category-filtered product lists
CREATE INDEX products_category ON products(category_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX products_category;
DROP INDEX lines_norm;
DROP INDEX lines_parent;
