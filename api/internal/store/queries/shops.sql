-- name: ListShops :many
SELECT * FROM shops
WHERE deleted_at IS NULL
ORDER BY name;

-- name: GetShop :one
SELECT * FROM shops
WHERE id = ? AND deleted_at IS NULL;

-- name: CreateShop :one
INSERT INTO shops (name, chain, branch, address, city, notes, domain, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateShop :one
UPDATE shops
SET name = ?, chain = ?, branch = ?, address = ?, city = ?, notes = ?, domain = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteShop :execrows
UPDATE shops
SET deleted_at = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL;

-- name: RejectShopSuggestion :exec
INSERT INTO shop_suggestion_rejections (branch_key, created_at) VALUES (?, ?)
ON CONFLICT (branch_key) DO NOTHING;

-- name: IsShopSuggestionRejected :one
SELECT COUNT(*) FROM shop_suggestion_rejections WHERE branch_key = ?;
