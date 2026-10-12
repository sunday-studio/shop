-- name: ListDatedReceiptsForDuplicates :many
-- Every parsed receipt with a date and a total: the pool for duplicate detection.
SELECT id, shop_id, purchased_at, total_cents, status FROM receipts
WHERE total_cents IS NOT NULL AND total_cents > 0 AND purchased_at IS NOT NULL
  AND status IN ('ok', 'needs_review')
ORDER BY id;

-- name: ListDuplicateCandidates :many
-- Older receipts with the same printed total on a given local day (the caller passes the UTC bounds).
SELECT id, shop_id, purchased_at, total_cents, status FROM receipts
WHERE total_cents = ? AND purchased_at >= ? AND purchased_at < ?
  AND id < ? AND status IN ('ok', 'needs_review')
ORDER BY id;

-- name: ListItemNormsForReceipts :many
SELECT receipt_id, norm_text FROM receipt_lines
WHERE kind = 'item' AND norm_text != '' AND receipt_id IN (sqlc.slice('ids'))
ORDER BY receipt_id, line_no;

-- name: DismissDuplicate :exec
INSERT INTO receipt_duplicate_dismissals (receipt_id, dismissed_at) VALUES (?, ?)
ON CONFLICT (receipt_id) DO NOTHING;

-- name: UndismissDuplicate :exec
DELETE FROM receipt_duplicate_dismissals WHERE receipt_id = ?;

-- name: IsDuplicateDismissed :one
SELECT COUNT(*) FROM receipt_duplicate_dismissals WHERE receipt_id = ?;

-- name: ListDuplicateDismissals :many
SELECT receipt_id FROM receipt_duplicate_dismissals;
