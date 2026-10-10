-- name: ListShops :many
SELECT * FROM shops
WHERE deleted_at IS NULL
ORDER BY name;

-- name: GetShop :one
SELECT * FROM shops
WHERE id = ? AND deleted_at IS NULL;

-- name: CreateShop :one
INSERT INTO shops (name, chain, branch, address, city, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateShop :one
UPDATE shops
SET name = ?, chain = ?, branch = ?, address = ?, city = ?, notes = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteShop :execrows
UPDATE shops
SET deleted_at = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL;
