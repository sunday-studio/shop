-- name: CreateReceipt :one
INSERT INTO receipts (shop_id, purchased_at, date_locked, image_path, image_sha256, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)
RETURNING *;

-- name: GetReceipt :one
SELECT * FROM receipts WHERE id = ?;

-- name: GetReceiptBySHA :one
SELECT * FROM receipts WHERE image_sha256 = ?;

-- name: ListReceipts :many
SELECT sqlc.embed(receipts),
  CAST((SELECT COUNT(*) FROM receipt_lines l
        WHERE l.receipt_id = receipts.id AND l.kind = 'item') AS INTEGER) AS line_count,
  CAST((SELECT COUNT(*) FROM receipt_lines l
        WHERE l.receipt_id = receipts.id AND l.kind = 'item' AND l.product_id IS NULL) AS INTEGER) AS unlinked_count
FROM receipts
WHERE (sqlc.narg(shop_id) IS NULL OR shop_id = sqlc.narg(shop_id))
  AND (sqlc.narg(status) IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(from_ts) IS NULL OR purchased_at >= sqlc.narg(from_ts))
  AND (sqlc.narg(to_ts) IS NULL OR purchased_at < sqlc.narg(to_ts))
  AND (sqlc.narg(before_id) IS NULL OR id < sqlc.narg(before_id))
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: CountReceiptsByStatus :many
SELECT status, COUNT(*) AS n FROM receipts GROUP BY status;

-- name: ListReceiptIDsForReparse :many
SELECT id FROM receipts
WHERE confirmed_at IS NULL
  AND raw_ocr_json IS NOT NULL
  AND (sqlc.narg(status) IS NULL OR status = sqlc.narg(status))
  AND header_edited = 0
  AND NOT EXISTS (SELECT 1 FROM receipt_lines l WHERE l.receipt_id = receipts.id AND l.user_edited = 1)
ORDER BY id;

-- name: SetReceiptStatus :exec
UPDATE receipts SET status = ?, updated_at = ? WHERE id = ?;

-- name: SetReceiptOCR :exec
UPDATE receipts SET ocr_engine = ?, ocr_ms = ?, raw_ocr_json = ?, updated_at = ? WHERE id = ?;

-- name: SetReceiptParsed :exec
-- All named args: mixing sqlc.narg with ? misnumbers parameters on SQLite.
UPDATE receipts
SET purchased_at = CASE WHEN date_locked = 1 THEN purchased_at
                    ELSE COALESCE(sqlc.narg(purchased_at), purchased_at) END,
    total_cents = sqlc.narg(total_cents),
    parsed_sum_cents = sqlc.narg(parsed_sum_cents),
    payment_method = sqlc.narg(payment_method),
    status = sqlc.arg(status),
    review_reasons = sqlc.narg(review_reasons),
    parser_version = sqlc.narg(parser_version),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateReceiptHeader :one
UPDATE receipts
SET shop_id = ?, purchased_at = ?, date_locked = ?, total_cents = ?, header_edited = 1, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: ConfirmReceipt :one
UPDATE receipts SET status = 'ok', confirmed_at = ?, updated_at = ? WHERE id = ?
RETURNING *;

-- name: DeleteReceipt :execrows
DELETE FROM receipts WHERE id = ?;

-- name: CountReceiptsByImagePath :one
SELECT COUNT(*) FROM receipts WHERE image_path = ?;

-- name: ClearHeaderEdited :exec
UPDATE receipts SET header_edited = 0, confirmed_at = NULL, updated_at = ? WHERE id = ?;

-- name: SetReview :exec
UPDATE receipts SET parsed_sum_cents = ?, review_reasons = ?, status = ?, updated_at = ? WHERE id = ?;
