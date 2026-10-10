-- name: ListLines :many
SELECT * FROM receipt_lines WHERE receipt_id = ? ORDER BY line_no, id;

-- name: GetLine :one
SELECT * FROM receipt_lines WHERE id = ?;

-- name: InsertLine :one
INSERT INTO receipt_lines (
  receipt_id, line_no, raw_text, norm_text, kind, qty, unit,
  unit_price_cents, line_total_cents, vat_code, parent_line_id, product_id, user_edited)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateLine :one
UPDATE receipt_lines
SET raw_text = ?, norm_text = ?, kind = ?, qty = ?, unit = ?, unit_price_cents = ?,
    line_total_cents = ?, vat_code = ?, parent_line_id = ?, user_edited = 1
WHERE id = ?
RETURNING *;

-- name: DeleteLine :execrows
DELETE FROM receipt_lines WHERE id = ?;

-- name: DeleteLinesForReceipt :exec
DELETE FROM receipt_lines WHERE receipt_id = ?;

-- name: MaxLineNo :one
SELECT CAST(COALESCE(MAX(line_no), 0) AS INTEGER) FROM receipt_lines WHERE receipt_id = ?;

-- name: SumLines :one
SELECT CAST(COALESCE(SUM(line_total_cents), 0) AS INTEGER) FROM receipt_lines WHERE receipt_id = ?;
