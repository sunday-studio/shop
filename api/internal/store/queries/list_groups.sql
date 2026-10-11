-- name: CreateShoppingGroup :one
INSERT INTO shopping_list_groups (name, kind, note, source, external_ref, sort, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: RestoreShoppingGroup :one
INSERT INTO shopping_list_groups (id, name, kind, note, source, external_ref, sort, archived_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetShoppingGroup :one
SELECT * FROM shopping_list_groups WHERE id = ?;

-- name: FindShoppingGroupByRef :one
SELECT * FROM shopping_list_groups WHERE source = ? AND external_ref = ?;

-- name: ListShoppingGroups :many
SELECT * FROM shopping_list_groups
WHERE (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind))
  AND (sqlc.arg(include_archived) = 1 OR archived_at IS NULL)
ORDER BY archived_at IS NOT NULL, sort, id;

-- name: ShoppingGroupsChangedSince :many
SELECT * FROM shopping_list_groups WHERE updated_at >= ? ORDER BY updated_at, id;

-- name: ShoppingGroupsChangedSinceRev :many
SELECT * FROM shopping_list_groups WHERE rev > ? ORDER BY rev;

-- name: UpdateShoppingGroup :one
UPDATE shopping_list_groups SET name = ?, note = ?, sort = ?, archived_at = ?, updated_at = ? WHERE id = ?
RETURNING *;

-- name: TouchShoppingGroup :exec
UPDATE shopping_list_groups SET updated_at = ? WHERE id = ?;

-- name: DeleteShoppingGroup :execrows
DELETE FROM shopping_list_groups WHERE id = ?;

-- name: MaxShoppingGroupSort :one
SELECT CAST(COALESCE(MAX(sort), 0) AS INTEGER) FROM shopping_list_groups;

-- name: ListGroupItems :many
SELECT * FROM shopping_list_items WHERE group_id = ? ORDER BY created_at, id;

-- name: MoveShoppingItem :one
UPDATE shopping_list_items SET group_id = ?, updated_at = ? WHERE id = ?
RETURNING *;

-- name: ShoppingGroupCounts :many
SELECT group_id, COUNT(*) AS item_count, CAST(COALESCE(SUM(checked_at IS NOT NULL), 0) AS INTEGER) AS checked_count
FROM shopping_list_items WHERE group_id IS NOT NULL GROUP BY group_id;

-- name: PutGroupTrash :exec
INSERT INTO shopping_list_group_trash (group_id, mode, snapshot, deleted_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (group_id) DO UPDATE SET mode = excluded.mode, snapshot = excluded.snapshot,
  deleted_at = excluded.deleted_at, restored_at = NULL;

-- name: GetGroupTrash :one
SELECT * FROM shopping_list_group_trash WHERE group_id = ?;

-- name: MarkGroupTrashRestored :exec
UPDATE shopping_list_group_trash SET restored_at = ? WHERE group_id = ?;

-- name: PruneGroupTrash :exec
DELETE FROM shopping_list_group_trash WHERE deleted_at < ?;
