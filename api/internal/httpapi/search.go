package httpapi

import (
	"net/http"

	"shop/internal/apitypes"
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
	prods := make([]apitypes.Product, len(res.Products))
	for i, p := range res.Products {
		prods[i] = toProduct(p.Product, nil)
		sc := round3(p.Score)
		prods[i].Score = &sc
	}
	if err := s.withSummaries(r, prods); err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := struct {
		Products []apitypes.Product `json:"products"`
		Aliases  []alias            `json:"aliases"`
		Receipts []receiptHit       `json:"receipts"`
	}{Products: prods, Aliases: []alias{}, Receipts: []receiptHit{}}
	for _, a := range res.Aliases {
		out.Aliases = append(out.Aliases, alias{Text: a.Text, ProductID: a.ProductID, ProductName: a.ProductName,
			ShopID: a.ShopID, ShopName: a.ShopName, Score: round3(a.Score)})
	}
	for _, h := range res.Receipts {
		out.Receipts = append(out.Receipts, receiptHit{Receipt: toReceipt(h.Receipt, nil), MatchedLines: h.MatchedLines})
	}
	writeJSON(w, http.StatusOK, out)
}
