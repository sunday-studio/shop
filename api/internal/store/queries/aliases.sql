-- name: LookupAlias :one
-- Shop-specific alias wins over a global (shop_id NULL) one.
SELECT a.product_id FROM product_aliases a
JOIN products p ON p.id = a.product_id AND p.deleted_at IS NULL
WHERE a.norm_text = sqlc.arg(norm_text)
  AND (a.shop_id = sqlc.arg(shop_id) OR a.shop_id IS NULL)
ORDER BY a.shop_id IS NULL
LIMIT 1;
