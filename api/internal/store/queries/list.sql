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
UPDATE shopping_list_items
SET checked_at = ?, updated_at = ?, check_source = 'manual', checked_receipt_id = NULL, checked_line_id = NULL
WHERE id = ?
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

-- name: CheckShoppingItemFromReceipt :one
UPDATE shopping_list_items
SET checked_at = ?, updated_at = ?, check_source = 'receipt', checked_receipt_id = ?, checked_line_id = ?
WHERE id = ? AND checked_at IS NULL
RETURNING *;

-- name: SetShoppingItemQtyNote :one
UPDATE shopping_list_items SET qty = ?, note = ?, updated_at = ? WHERE id = ?
RETURNING *;

-- name: InsertReceiptCheck :execrows
INSERT INTO shopping_list_receipt_checks (receipt_id, item_id, kind, line_id, bought_qty, prev_qty, prev_note, set_note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (receipt_id, item_id) DO NOTHING;

-- name: ActiveReceiptChecks :many
SELECT * FROM shopping_list_receipt_checks WHERE receipt_id = ? AND undone_at IS NULL ORDER BY id DESC;

-- name: MarkReceiptCheckUndone :exec
UPDATE shopping_list_receipt_checks SET undone_at = ? WHERE id = ?;

-- name: PurgeReceiptChecks :exec
DELETE FROM shopping_list_receipt_checks WHERE receipt_id = ?;

-- name: PurgeReceiptSuggestions :exec
DELETE FROM shopping_list_suggestions WHERE receipt_id = ?;

-- name: InsertListSuggestion :execrows
INSERT INTO shopping_list_suggestions (item_id, receipt_id, line_id, score, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (item_id, receipt_id) DO NOTHING;

-- name: GetListSuggestion :one
SELECT * FROM shopping_list_suggestions WHERE id = ?;

-- name: ListOpenSuggestions :many
SELECT s.* FROM shopping_list_suggestions s
JOIN shopping_list_items i ON i.id = s.item_id
WHERE s.state = 'open' AND i.checked_at IS NULL
ORDER BY s.id;

-- name: ListReceiptSuggestions :many
SELECT * FROM shopping_list_suggestions WHERE receipt_id = ? AND state = 'open' ORDER BY id;

-- name: ResolveListSuggestion :exec
UPDATE shopping_list_suggestions SET state = ?, resolved_at = ? WHERE id = ?;

-- name: ListLinesOfReceipt :many
SELECT l.*, COALESCE(p.name, '') AS product_name, COALESCE(p.name_en, '') AS product_name_en,
       p.category_id AS product_category_id
FROM receipt_lines l LEFT JOIN products p ON p.id = l.product_id
WHERE l.receipt_id = ? AND l.kind = 'item'
ORDER BY l.line_no, l.id;
