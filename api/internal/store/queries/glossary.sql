-- name: ListGlossaryTerms :many
SELECT * FROM glossary_terms ORDER BY dutch;

-- name: GetGlossaryTerm :one
SELECT * FROM glossary_terms WHERE dutch = ?;

-- name: UpsertGlossaryTerm :one
INSERT INTO glossary_terms (dutch, english, keep_as_is, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (dutch) DO UPDATE SET english = excluded.english, keep_as_is = excluded.keep_as_is,
  updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteGlossaryTerm :execrows
DELETE FROM glossary_terms WHERE dutch = ?;

-- name: ProductsForTranslation :many
-- Products whose English name is automatic (dictionary or none yet).
SELECT id, name, name_en, name_en_source FROM products
WHERE deleted_at IS NULL AND (name_en_source IS NULL OR name_en_source = 'dictionary')
ORDER BY id;

-- name: ProductNames :many
SELECT id, name FROM products WHERE deleted_at IS NULL;

-- name: LatestPurchases :many
-- Shop and time of each product's most recent purchase.
SELECT l.product_id AS product_id, s.name AS shop_name, s.chain AS shop_chain,
       CAST(MAX(COALESCE(r.purchased_at, r.created_at)) AS TEXT) AS at
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
JOIN shops s ON s.id = r.shop_id
WHERE l.product_id IS NOT NULL
GROUP BY l.product_id;

-- name: UnmatchedItemTexts :many
SELECT raw_text FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
WHERE l.product_id IS NULL AND l.kind = 'item'
  AND COALESCE(r.purchased_at, r.created_at) >= sqlc.arg(since);
