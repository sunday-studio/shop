-- name: ListCategories :many
SELECT * FROM categories WHERE deleted_at IS NULL ORDER BY name COLLATE NOCASE;

-- name: ListCategoriesWithCounts :many
SELECT c.*, CAST((SELECT COUNT(*) FROM products p WHERE p.category_id = c.id AND p.deleted_at IS NULL) AS INTEGER) AS product_count
FROM categories c WHERE c.deleted_at IS NULL ORDER BY c.name COLLATE NOCASE;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = ? AND deleted_at IS NULL;

-- name: FindCategoriesByName :many
-- Case-insensitive, including soft-deleted rows.
SELECT * FROM categories WHERE name = ? COLLATE NOCASE ORDER BY deleted_at IS NOT NULL, id;

-- name: CreateCategory :one
INSERT INTO categories (name, sort, color) VALUES (?, 0, ?)
RETURNING *;

-- name: UpdateCategory :one
UPDATE categories SET name = ?, color = ? WHERE id = ? AND deleted_at IS NULL
RETURNING *;

-- name: ReviveCategory :one
UPDATE categories SET deleted_at = NULL, name = ?, color = ? WHERE id = ?
RETURNING *;

-- name: TombstoneCategoryName :exec
UPDATE categories SET name = name || ' (deleted ' || id || ')' WHERE id = ?;

-- name: SoftDeleteCategory :execrows
UPDATE categories SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: CountCategoryProducts :one
SELECT COUNT(*) FROM products WHERE category_id = ? AND deleted_at IS NULL;

-- name: MoveCategoryProducts :execrows
UPDATE products SET category_id = sqlc.narg(to_category), category_source = sqlc.narg(source), updated_at = sqlc.arg(updated_at)
WHERE category_id = sqlc.arg(from_category) AND deleted_at IS NULL;

-- name: RepointCategoryKeywords :exec
UPDATE category_keywords SET category_id = ? WHERE category_id = ?;

-- name: DeleteCategoryKeywords :exec
DELETE FROM category_keywords WHERE category_id = ?;

-- name: ListCategoryKeywords :many
SELECT * FROM category_keywords ORDER BY keyword;
