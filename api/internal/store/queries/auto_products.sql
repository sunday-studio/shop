-- name: InsertAutoProduct :one
INSERT INTO products (name, name_en, name_en_source, brand, category_id, barcode, net_quantity, net_unit, off_code,
                      created_via, needs_review, review_reason, suggested_merge_id, category_source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetProductAny :one
SELECT * FROM products WHERE id = ?;

-- name: SetProductReview :exec
UPDATE products SET needs_review = ?, review_reason = ?, suggested_merge_id = ?, updated_at = ? WHERE id = ?;

-- name: SetProductCategory :exec
UPDATE products SET category_id = ?, category_source = ?, updated_at = ? WHERE id = ?;

-- name: SetProductNameManual :exec
UPDATE products SET name = ?, updated_at = ? WHERE id = ?;

-- name: IsDistinctPair :one
SELECT COUNT(*) FROM product_distinct WHERE a_id = ? AND b_id = ?;

-- name: InsertDistinctPair :exec
INSERT INTO product_distinct (a_id, b_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;

-- name: DeleteDistinctPair :exec
DELETE FROM product_distinct WHERE a_id = ? AND b_id = ?;

-- name: ReviewQueue :many
SELECT p.* FROM products p
WHERE p.needs_review = 1 AND p.deleted_at IS NULL
  AND (sqlc.narg(reason) IS NULL OR p.review_reason = sqlc.narg(reason))
ORDER BY CASE p.review_reason
           WHEN 'near_match' THEN 1 WHEN 'barcode_name_match' THEN 2 WHEN 'odd_name' THEN 3
           WHEN 'no_category' THEN 4 WHEN 'barcode_minimal' THEN 5 ELSE 6 END,
         p.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ReviewCounts :many
SELECT CAST(COALESCE(review_reason, '') AS TEXT) AS reason, COUNT(*) AS n
FROM products WHERE needs_review = 1 AND deleted_at IS NULL GROUP BY review_reason;

-- name: SourceLines :many
SELECT l.id AS line_id, l.receipt_id, l.raw_text, l.line_total_cents,
       CAST(COALESCE(s.name, 'Unassigned') AS TEXT) AS shop_name, r.purchased_at, r.created_at
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
LEFT JOIN shops s ON s.id = r.shop_id
WHERE l.product_id = ? AND l.kind = 'item'
ORDER BY COALESCE(r.purchased_at, r.created_at) DESC, l.id DESC
LIMIT ?;

-- name: CountSourceLines :one
SELECT COUNT(*) FROM receipt_lines WHERE product_id = ? AND kind = 'item';

-- name: ListLineIDsByProduct :many
SELECT id FROM receipt_lines WHERE product_id = ? ORDER BY id;

-- name: MoveLineIfLinked :execrows
UPDATE receipt_lines SET product_id = sqlc.narg(to_product) WHERE id = sqlc.arg(id) AND product_id IS sqlc.narg(expected);

-- name: ListAliasesByProduct :many
SELECT * FROM product_aliases WHERE product_id = ? ORDER BY id;

-- name: MoveAliasIfOwned :execrows
UPDATE product_aliases SET product_id = sqlc.arg(to_product) WHERE id = sqlc.arg(id) AND product_id = sqlc.arg(expected);

-- name: InsertAliasRow :exec
INSERT OR IGNORE INTO product_aliases (id, shop_id, norm_text, product_id, created_at) VALUES (?, ?, ?, ?, ?);

-- name: MarkProductMerged :exec
UPDATE products SET deleted_at = ?, merged_into_id = ?, barcode = NULL, needs_review = 0, review_reason = NULL,
                    suggested_merge_id = NULL, updated_at = ? WHERE id = ?;

-- name: MarkProductDiscarded :exec
UPDATE products SET deleted_at = ?, barcode = NULL, needs_review = 0, review_reason = NULL,
                    suggested_merge_id = NULL, updated_at = ? WHERE id = ?;

-- name: RestoreProduct :exec
UPDATE products SET deleted_at = NULL, merged_into_id = NULL, barcode = ?, needs_review = ?, review_reason = ?,
                    suggested_merge_id = ?, updated_at = ? WHERE id = ?;

-- name: FillProductFields :exec
UPDATE products SET barcode = ?, brand = ?, image_path = ?, net_quantity = ?, net_unit = ?, off_code = ?,
                    category_id = ?, category_source = ?, updated_at = ? WHERE id = ?;

-- name: ClearSuggestionsPointingAt :exec
UPDATE products SET needs_review = CASE WHEN review_reason = 'near_match' THEN 0 ELSE needs_review END,
                    review_reason = CASE WHEN review_reason = 'near_match' THEN NULL ELSE review_reason END,
                    suggested_merge_id = NULL, updated_at = ?
WHERE suggested_merge_id = ?;

-- name: InsertMerge :one
INSERT INTO product_merges (from_product_id, into_product_id, line_ids, alias_ids, alias_rows, into_fields_before,
                            from_review_before, kind, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMerge :one
SELECT * FROM product_merges WHERE id = ?;

-- name: MarkMergeUndone :exec
UPDATE product_merges SET undone_at = ? WHERE id = ?;

-- name: ListProductsWithoutBarcodeForMerge :many
SELECT id, name FROM products WHERE deleted_at IS NULL AND barcode IS NULL AND created_via IN ('receipt', 'backfill', 'manual');

-- name: ConfirmedReceiptIDs :many
SELECT id FROM receipts WHERE status = 'ok' AND confirmed_at IS NOT NULL AND id > sqlc.arg(after)
ORDER BY COALESCE(purchased_at, created_at), id;

-- name: CountConfirmedReceipts :one
SELECT COUNT(*) FROM receipts WHERE status = 'ok' AND confirmed_at IS NOT NULL;

-- name: ItemLinesToLink :many
SELECT * FROM receipt_lines
WHERE receipt_id = ? AND kind = 'item' AND product_id IS NULL AND user_edited = 0
ORDER BY line_no;

-- name: ListLinesForSplit :many
SELECT l.id FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id
WHERE l.product_id = sqlc.arg(product_id) AND l.norm_text = sqlc.arg(norm_text) AND r.shop_id IS sqlc.narg(shop_id);

-- name: ListAliasesForSplit :many
SELECT * FROM product_aliases
WHERE product_id = sqlc.arg(product_id) AND shop_id IS sqlc.narg(shop_id) AND norm_text IN (sqlc.arg(norm_a), sqlc.arg(norm_b));

-- name: ListDiscardedLineIDs :many
-- Lines whose product the user discarded in the review queue (not undone): loading products from receipts
-- must not bring those products back.
SELECT line_ids FROM product_merges WHERE kind = 'discard' AND undone_at IS NULL;

-- name: UnlinkedItemLinesOfConfirmed :many
-- Item lines of confirmed receipts that have no product yet (candidates for loading products from receipts).
SELECT l.id, l.receipt_id, l.raw_text FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id
WHERE r.status = 'ok' AND r.confirmed_at IS NOT NULL AND l.kind = 'item' AND l.product_id IS NULL AND l.user_edited = 0;
