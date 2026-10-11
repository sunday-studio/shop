-- name: ListShoppingItems :many
-- Template items are blueprints, not things to buy, so they never show up in the list.
SELECT i.* FROM shopping_list_items i
LEFT JOIN shopping_list_groups g ON g.id = i.group_id
WHERE (g.id IS NULL OR g.kind <> 'template')
  AND (i.checked_at IS NULL OR (sqlc.arg(include_checked) = 1))
ORDER BY i.checked_at IS NOT NULL, i.created_at, i.id;

-- name: ShoppingItemsChangedSince :many
SELECT i.* FROM shopping_list_items i
LEFT JOIN shopping_list_groups g ON g.id = i.group_id
WHERE i.updated_at >= ? AND (g.id IS NULL OR g.kind <> 'template')
ORDER BY i.updated_at, i.id;

-- name: ShoppingItemsChangedSinceRev :many
SELECT i.* FROM shopping_list_items i
LEFT JOIN shopping_list_groups g ON g.id = i.group_id
WHERE i.rev > ? AND (g.id IS NULL OR g.kind <> 'template')
ORDER BY i.rev;

-- name: ShoppingTombstonesSince :many
SELECT kind, id FROM shopping_list_tombstones WHERE deleted_at >= ? ORDER BY id;

-- name: ShoppingTombstonesSinceRev :many
SELECT kind, id FROM shopping_list_tombstones WHERE rev > ? ORDER BY rev;

-- name: MaxPrunableTombstoneRev :one
SELECT CAST(COALESCE(MAX(rev), 0) AS INTEGER) FROM shopping_list_tombstones WHERE deleted_at < ?;

-- name: PruneShoppingTombstones :exec
DELETE FROM shopping_list_tombstones WHERE deleted_at < ?;

-- name: RaisePrunedRev :exec
UPDATE shopping_list_rev SET pruned_rev = MAX(pruned_rev, ?) WHERE id = 1;

-- name: GetListRev :one
SELECT rev, pruned_rev FROM shopping_list_rev WHERE id = 1;

-- name: GetShoppingItem :one
SELECT * FROM shopping_list_items WHERE id = ?;

-- name: CreateShoppingItem :one
INSERT INTO shopping_list_items (product_id, text, qty, note, group_id, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: RestoreShoppingItem :one
INSERT INTO shopping_list_items (id, product_id, text, qty, note, checked_at, group_id, source, created_at, updated_at,
                                 check_source, checked_receipt_id, checked_line_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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

-- name: RecordShoppingTombstone :exec
INSERT INTO shopping_list_tombstones (kind, id, deleted_at) VALUES (?, ?, ?)
ON CONFLICT (kind, id) DO UPDATE SET deleted_at = excluded.deleted_at;

-- name: ClearShoppingTombstone :exec
DELETE FROM shopping_list_tombstones WHERE kind = ? AND id = ?;

-- name: CheckedShoppingItemIDs :many
SELECT id FROM shopping_list_items WHERE checked_at IS NOT NULL AND group_id IS NULL;

-- name: FindOpenShoppingItemByProduct :one
-- One open item per product and group (group NULL = the main list).
SELECT * FROM shopping_list_items WHERE product_id = ? AND checked_at IS NULL AND group_id IS sqlc.narg(group_id) LIMIT 1;

-- name: OpenGroupsOfProduct :many
-- Where else a product is open: group id, NULL for the main list. Templates and archived groups do not count.
SELECT i.group_id FROM shopping_list_items i
LEFT JOIN shopping_list_groups g ON g.id = i.group_id
WHERE i.product_id = ? AND i.checked_at IS NULL AND i.id <> ?
  AND (g.id IS NULL OR (g.kind = 'checklist' AND g.archived_at IS NULL))
ORDER BY i.group_id;

-- name: GetClientID :one
SELECT item_id, kind FROM shopping_list_client_ids WHERE client_id = ?;

-- name: PutClientID :exec
INSERT INTO shopping_list_client_ids (client_id, item_id, kind, created_at) VALUES (?, ?, ?, ?);

-- name: CheckShoppingItemFromReceipt :one
UPDATE shopping_list_items
SET checked_at = ?, updated_at = ?, check_source = 'receipt', checked_receipt_id = ?, checked_line_id = ?
WHERE id = ? AND checked_at IS NULL
RETURNING *;

-- name: ListReconcilableItems :many
-- Open items a receipt may check: main list and live checklists (not templates, not archived groups).
SELECT i.* FROM shopping_list_items i
LEFT JOIN shopping_list_groups g ON g.id = i.group_id
WHERE i.checked_at IS NULL AND (g.id IS NULL OR (g.kind = 'checklist' AND g.archived_at IS NULL))
ORDER BY i.created_at, i.id;

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
