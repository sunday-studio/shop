-- name: ListShoppingItems :many
SELECT * FROM shopping_list_items
WHERE checked_at IS NULL OR (sqlc.arg(include_checked) = 1)
ORDER BY checked_at IS NOT NULL, created_at, id;

-- name: GetShoppingItem :one
SELECT * FROM shopping_list_items WHERE id = ?;

-- name: CreateShoppingItem :one
INSERT INTO shopping_list_items (product_id, text, qty, note, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateShoppingItem :one
UPDATE shopping_list_items SET product_id = ?, text = ?, qty = ?, note = ? WHERE id = ?
RETURNING *;

-- name: SetShoppingItemChecked :one
UPDATE shopping_list_items SET checked_at = ? WHERE id = ?
RETURNING *;

-- name: DeleteShoppingItem :execrows
DELETE FROM shopping_list_items WHERE id = ?;

-- name: ClearCheckedShoppingItems :execrows
DELETE FROM shopping_list_items WHERE checked_at IS NOT NULL;

-- name: FindOpenShoppingItemByProduct :one
SELECT * FROM shopping_list_items WHERE product_id = ? AND checked_at IS NULL LIMIT 1;
