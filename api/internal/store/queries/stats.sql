-- Spend counts receipts that were read (ok) or are awaiting review: the
-- money was spent either way. Undated receipts fall back to upload time.

-- name: SpendReceipts :many
SELECT r.id, r.shop_id, s.name AS shop_name,
       CAST(COALESCE(r.purchased_at, r.created_at) AS TEXT) AS at,
       CAST(COALESCE(r.total_cents, r.parsed_sum_cents, 0) AS INTEGER) AS total_cents
FROM receipts r JOIN shops s ON s.id = r.shop_id
WHERE r.status IN ('ok', 'needs_review')
  AND COALESCE(r.purchased_at, r.created_at) >= sqlc.arg(from_ts)
  AND COALESCE(r.purchased_at, r.created_at) < sqlc.arg(to_ts)
  AND (sqlc.narg(shop_id) IS NULL OR r.shop_id = sqlc.narg(shop_id))
ORDER BY at;

-- name: SpendLines :many
-- Lines with the product and category they count towards (discounts
-- inherit both from their parent item).
SELECT r.id AS receipt_id, r.shop_id, s.name AS shop_name,
       CAST(COALESCE(r.purchased_at, r.created_at) AS TEXT) AS at,
       l.kind, l.qty, l.unit, l.line_total_cents,
       p.id AS product_id, p.name AS product_name,
       c.id AS category_id, c.name AS category_name
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
JOIN shops s ON s.id = r.shop_id
LEFT JOIN receipt_lines pl ON pl.id = l.parent_line_id
LEFT JOIN products p ON p.id = COALESCE(l.product_id, pl.product_id)
LEFT JOIN categories c ON c.id = p.category_id
WHERE r.status IN ('ok', 'needs_review')
  AND COALESCE(r.purchased_at, r.created_at) >= sqlc.arg(from_ts)
  AND COALESCE(r.purchased_at, r.created_at) < sqlc.arg(to_ts)
  AND (sqlc.narg(shop_id) IS NULL OR r.shop_id = sqlc.narg(shop_id));

-- name: ProductPricePoints :many
SELECT pp.line_id, pp.product_id, pp.shop_id, s.name AS shop_name,
       CAST(pp.purchased_at AS TEXT) AS purchased_at, pp.qty, pp.unit,
       CAST(pp.paid_cents AS INTEGER) AS paid_cents,
       p.name AS product_name, p.net_quantity, p.net_unit
FROM price_points pp
JOIN shops s ON s.id = pp.shop_id
JOIN products p ON p.id = pp.product_id
WHERE pp.purchased_at IS NOT NULL
  AND (sqlc.narg(product_id) IS NULL OR pp.product_id = sqlc.narg(product_id))
  AND (sqlc.narg(shop_id) IS NULL OR pp.shop_id = sqlc.narg(shop_id))
  AND p.deleted_at IS NULL
ORDER BY pp.purchased_at, pp.line_id;

-- name: ProductPurchases :many
-- One row per (product, receipt) for linked items since a date.
SELECT l.product_id, p.name AS product_name, r.id AS receipt_id,
       CAST(MIN(COALESCE(r.purchased_at, r.created_at)) AS TEXT) AS at
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
JOIN products p ON p.id = l.product_id AND p.deleted_at IS NULL
WHERE l.kind = 'item' AND r.status IN ('ok', 'needs_review')
  AND COALESCE(r.purchased_at, r.created_at) >= sqlc.arg(since)
GROUP BY l.product_id, r.id
ORDER BY l.product_id, at;

-- name: ProductPurchaseSummary :many
-- Per product: receipts it was bought on, and the most recent purchase.
SELECT l.product_id AS product_id,
       CAST(COUNT(DISTINCT r.id) AS INTEGER) AS purchases,
       CAST(MAX(COALESCE(r.purchased_at, r.created_at)) AS TEXT) AS last_at
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
WHERE l.kind = 'item' AND l.product_id IS NOT NULL AND r.status IN ('ok', 'needs_review')
GROUP BY l.product_id;
