-- name: ListCategories :many
SELECT * FROM categories WHERE deleted_at IS NULL ORDER BY sort, name;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = ? AND deleted_at IS NULL;

-- name: CreateCategory :one
INSERT INTO categories (name, sort) VALUES (?, ?)
RETURNING *;

-- name: UpdateCategory :one
UPDATE categories SET name = ?, sort = ? WHERE id = ? AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteCategory :execrows
UPDATE categories SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: ListCategoryKeywords :many
SELECT * FROM category_keywords ORDER BY keyword;
