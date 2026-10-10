-- name: ListShoppingItems :many
SELECT * FROM shopping_list_items
WHERE checked_at IS NULL OR (sqlc.arg(include_checked) = 1)
ORDER BY checked_at IS NOT NULL, created_at, id;

-- name: ShoppingItemsChangedSince :many
SELECT * FROM shopping_list_items WHERE updated_at >= ? ORDER BY updated_at, id;

-- name: ShoppingDeletionsSince :many
SELECT item_id FROM shopping_list_deletions WHERE deleted_at >= ? ORDER BY item_id;

-- name: PruneShoppingDeletions :exec
DELETE FROM shopping_list_deletions WHERE deleted_at < ?;

-- name: GetShoppingItem :one
SELECT * FROM shopping_list_items WHERE id = ?;

-- name: CreateShoppingItem :one
INSERT INTO shopping_list_items (product_id, text, qty, note, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateShoppingItem :one
UPDATE shopping_list_items SET product_id = ?, text = ?, qty = ?, note = ?, updated_at = ? WHERE id = ?
RETURNING *;

-- name: SetShoppingItemChecked :one
UPDATE shopping_list_items SET checked_at = ?, updated_at = ? WHERE id = ?
RETURNING *;

-- name: DeleteShoppingItem :execrows
DELETE FROM shopping_list_items WHERE id = ?;

-- name: RecordShoppingDeletion :exec
INSERT INTO shopping_list_deletions (item_id, deleted_at) VALUES (?, ?)
ON CONFLICT (item_id) DO UPDATE SET deleted_at = excluded.deleted_at;

-- name: CheckedShoppingItemIDs :many
SELECT id FROM shopping_list_items WHERE checked_at IS NOT NULL;

-- name: FindOpenShoppingItemByProduct :one
SELECT * FROM shopping_list_items WHERE product_id = ? AND checked_at IS NULL LIMIT 1;

-- name: GetClientID :one
SELECT item_id FROM shopping_list_client_ids WHERE client_id = ?;

-- name: PutClientID :exec
INSERT INTO shopping_list_client_ids (client_id, item_id, created_at) VALUES (?, ?, ?);
