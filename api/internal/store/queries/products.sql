-- name: ListProducts :many
SELECT * FROM products
WHERE deleted_at IS NULL
  AND (sqlc.narg(category_id) IS NULL OR category_id = sqlc.narg(category_id))
ORDER BY name
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: GetProduct :one
SELECT * FROM products WHERE id = ? AND deleted_at IS NULL;

-- name: GetProductsByIDs :many
SELECT * FROM products WHERE deleted_at IS NULL AND id IN (sqlc.slice(ids));

-- name: GetProductByBarcode :one
SELECT * FROM products WHERE barcode = ? AND deleted_at IS NULL;

-- name: CreateProduct :one
INSERT INTO products (name, brand, category_id, barcode, net_quantity, net_unit, off_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateProduct :one
UPDATE products
SET name = ?, brand = ?, category_id = ?, barcode = ?, net_quantity = ?, net_unit = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL
RETURNING *;

-- name: SetProductImage :exec
UPDATE products SET image_path = ?, updated_at = ? WHERE id = ?;

-- name: SoftDeleteProduct :execrows
-- Frees the barcode so the product can be re-created from a scan.
UPDATE products SET deleted_at = ?, barcode = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: GetNutrition :one
SELECT * FROM nutrition WHERE product_id = ?;

-- name: UpsertNutrition :one
INSERT INTO nutrition (product_id, per, energy_kcal, fat, saturated_fat, carbs, sugars, fibre, protein, salt, source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (product_id) DO UPDATE SET
  per = excluded.per, energy_kcal = excluded.energy_kcal, fat = excluded.fat,
  saturated_fat = excluded.saturated_fat, carbs = excluded.carbs, sugars = excluded.sugars,
  fibre = excluded.fibre, protein = excluded.protein, salt = excluded.salt, source = excluded.source
RETURNING *;

-- name: DeleteNutrition :exec
DELETE FROM nutrition WHERE product_id = ?;

-- name: UpsertAlias :one
INSERT INTO product_aliases (shop_id, norm_text, product_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (shop_id, norm_text) DO UPDATE SET product_id = excluded.product_id
RETURNING *;

-- name: FindGlobalAlias :one
-- UNIQUE(shop_id, norm_text) does not cover NULL shop_id, so global aliases
-- are deduplicated by hand.
SELECT * FROM product_aliases WHERE shop_id IS NULL AND norm_text = ?;

-- name: SetAliasProduct :exec
UPDATE product_aliases SET product_id = ? WHERE id = ?;

-- name: ListAliasesForProduct :many
SELECT a.*, s.name AS shop_name
FROM product_aliases a LEFT JOIN shops s ON s.id = a.shop_id
WHERE a.product_id = ?
ORDER BY a.created_at DESC;

-- name: GetAlias :one
SELECT * FROM product_aliases WHERE id = ?;

-- name: DeleteAlias :execrows
DELETE FROM product_aliases WHERE id = ?;

-- name: SetLineProduct :one
UPDATE receipt_lines SET product_id = ? WHERE id = ?
RETURNING *;

-- name: LinkMatchingLines :execrows
-- Apply a newly learned alias to other unmatched lines from the same shop.
UPDATE receipt_lines
SET product_id = sqlc.arg(product_id)
WHERE product_id IS NULL
  AND kind = 'item'
  AND norm_text = sqlc.arg(norm_text)
  AND receipt_id IN (SELECT id FROM receipts WHERE shop_id = sqlc.arg(shop_id));

-- name: RecentUnmatchedLines :many
SELECT l.*, r.shop_id, r.purchased_at, s.name AS shop_name
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
JOIN shops s ON s.id = r.shop_id
WHERE l.product_id IS NULL
  AND l.kind = 'item'
  AND COALESCE(r.purchased_at, r.created_at) >= sqlc.arg(since)
ORDER BY COALESCE(r.purchased_at, r.created_at) DESC, l.line_no
LIMIT 500;
