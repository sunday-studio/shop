package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.writeErr(w, r, badf("q is required"))
		return
	}
	limit, err := s.intParam(r, "limit", 10, 1, 50)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Search.Search(r.Context(), q, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	type alias struct {
		Text        string  `json:"text"`
		ProductID   int64   `json:"product_id"`
		ProductName string  `json:"product_name"`
		ShopID      *int64  `json:"shop_id,omitempty"`
		ShopName    string  `json:"shop_name,omitempty"`
		Score       float64 `json:"score"`
	}
	type receiptHit struct {
		Receipt      any      `json:"receipt"`
		MatchedLines []string `json:"matched_lines"`
	}
	out := struct {
		Products []any        `json:"products"`
		Aliases  []alias      `json:"aliases"`
		Receipts []receiptHit `json:"receipts"`
	}{Products: []any{}, Aliases: []alias{}, Receipts: []receiptHit{}}
	for _, p := range res.Products {
		ap := toProduct(p.Product, nil)
		sc := round3(p.Score)
		ap.Score = &sc
		out.Products = append(out.Products, ap)
	}
	for _, a := range res.Aliases {
		out.Aliases = append(out.Aliases, alias{Text: a.Text, ProductID: a.ProductID, ProductName: a.ProductName,
			ShopID: a.ShopID, ShopName: a.ShopName, Score: round3(a.Score)})
	}
	for _, h := range res.Receipts {
		out.Receipts = append(out.Receipts, receiptHit{Receipt: toReceipt(h.Receipt, nil), MatchedLines: h.MatchedLines})
	}
	writeJSON(w, http.StatusOK, out)
}
