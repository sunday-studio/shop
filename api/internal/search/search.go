// Package search is the app's unified search: products (Dutch or English
// names), learned receipt texts (aliases) and receipts, in one call.
package search

import (
	"context"
	"database/sql"
	"strings"

	"shop/internal/match"
	"shop/internal/products"
	"shop/internal/store/db"
)

type Service struct {
	conn     *sql.DB
	q        *db.Queries
	products *products.Service
	matcher  *match.Matcher
}

func New(conn *sql.DB, p *products.Service, m *match.Matcher) *Service {
	return &Service{conn: conn, q: db.New(conn), products: p, matcher: m}
}

// ReceiptHit is a receipt with the item lines that matched.
type ReceiptHit struct {
	Receipt      db.Receipt
	MatchedLines []string
}

type Results struct {
	Products []products.SearchResult
	Aliases  []match.AliasHit
	Receipts []ReceiptHit
}

const maxMatchedLines = 3

// Search runs all three searches. Receipts match on line text, or because
// a line is linked to one of the matched products (so "milk" finds receipts
// that say "MELK").
func (s *Service) Search(ctx context.Context, q string, limit int) (Results, error) {
	var out Results
	q = strings.TrimSpace(q)
	if q == "" {
		return out, nil
	}
	var err error
	if out.Products, err = s.products.List(ctx, q, nil, limit, 0); err != nil {
		return out, err
	}
	if out.Aliases, err = s.matcher.SearchAliases(ctx, q, limit, 0.6); err != nil {
		return out, err
	}
	ids := make([]int64, 0, len(out.Products))
	for _, p := range out.Products {
		ids = append(ids, p.ID)
	}
	out.Receipts, err = s.receipts(ctx, q, ids, limit)
	return out, err
}

func likePattern(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
	return "%" + s + "%"
}

func (s *Service) receipts(ctx context.Context, q string, productIDs []int64, limit int) ([]ReceiptHit, error) {
	norm := match.Normalise(q)
	if norm == "" && len(productIDs) == 0 {
		return nil, nil
	}
	args := []any{likePattern(norm)}
	cond := `l.norm_text LIKE ? ESCAPE '\'`
	if norm == "" {
		cond = "0"
		args = nil
	}
	if len(productIDs) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(productIDs)), ",")
		cond = "(" + cond + " OR l.product_id IN (" + ph + "))"
		for _, id := range productIDs {
			args = append(args, id)
		}
	}
	rows, err := s.conn.QueryContext(ctx, `
		SELECT l.receipt_id, l.raw_text
		FROM receipt_lines l
		WHERE l.kind = 'item' AND `+cond+`
		ORDER BY l.receipt_id DESC, l.line_no
		LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var order []int64
	lines := map[int64][]string{}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		if _, seen := lines[id]; !seen {
			if len(order) == limit {
				continue
			}
			order = append(order, id)
		}
		if len(lines[id]) < maxMatchedLines {
			lines[id] = append(lines[id], text)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ReceiptHit, 0, len(order))
	for _, id := range order {
		r, err := s.q.GetReceipt(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, ReceiptHit{Receipt: r, MatchedLines: lines[id]})
	}
	return out, nil
}
