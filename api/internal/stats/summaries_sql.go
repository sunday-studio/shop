package stats

import (
	"context"
	"strings"

	"shop/internal/store/db"
)

// placeholders returns "?,?,?" and the ids as arguments.
func placeholders(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// pricePointsFor is ProductPricePoints restricted to some products in SQL (sqlc cannot combine a slice
// with the optional filters, and loading every price point for a page of 50 products is what made the
// product list slow).
func (s *Service) pricePointsFor(ctx context.Context, ids []int64) ([]db.ProductPricePointsRow, error) {
	ph, args := placeholders(ids)
	rows, err := s.conn.QueryContext(ctx, `
SELECT pp.line_id, pp.product_id, pp.shop_id, s.name AS shop_name,
       CAST(pp.purchased_at AS TEXT) AS purchased_at, pp.qty, pp.unit,
       CAST(pp.paid_cents AS INTEGER) AS paid_cents,
       p.name AS product_name, p.net_quantity, p.net_unit
FROM price_points pp
JOIN shops s ON s.id = pp.shop_id
JOIN products p ON p.id = pp.product_id
WHERE pp.purchased_at IS NOT NULL AND pp.product_id IN (`+ph+`) AND p.deleted_at IS NULL
ORDER BY pp.purchased_at, pp.line_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.ProductPricePointsRow
	for rows.Next() {
		var r db.ProductPricePointsRow
		if err := rows.Scan(&r.LineID, &r.ProductID, &r.ShopID, &r.ShopName, &r.PurchasedAt, &r.Qty, &r.Unit,
			&r.PaidCents, &r.ProductName, &r.NetQuantity, &r.NetUnit); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// purchaseSummaryFor is ProductPurchaseSummary restricted to some products.
func (s *Service) purchaseSummaryFor(ctx context.Context, ids []int64) ([]db.ProductPurchaseSummaryRow, error) {
	ph, args := placeholders(ids)
	rows, err := s.conn.QueryContext(ctx, `
SELECT l.product_id AS product_id,
       CAST(COUNT(DISTINCT r.id) AS INTEGER) AS purchases,
       CAST(MAX(COALESCE(r.purchased_at, r.created_at)) AS TEXT) AS last_at
FROM receipt_lines l
JOIN receipts r ON r.id = l.receipt_id
WHERE l.kind = 'item' AND l.product_id IN (`+ph+`) AND r.status IN ('ok', 'needs_review')
GROUP BY l.product_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.ProductPurchaseSummaryRow
	for rows.Next() {
		var r db.ProductPurchaseSummaryRow
		if err := rows.Scan(&r.ProductID, &r.Purchases, &r.LastAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
