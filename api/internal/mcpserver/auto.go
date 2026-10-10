package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"shop/internal/autoprod"
)

type ReviewIn struct {
	Reason string `json:"reason,omitempty" jsonschema:"only this reason: near_match, barcode_name_match, odd_name, no_category, barcode_minimal, barcode_off_unavailable"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max items (default 20)"`
}

type ReviewCard struct {
	ProductID    int64   `json:"product_id"`
	Name         string  `json:"name"`
	NameEN       string  `json:"name_en,omitempty"`
	Reason       string  `json:"reason"`
	CreatedVia   string  `json:"created_via"`
	Barcode      string  `json:"barcode,omitempty"`
	SameAsID     int64   `json:"suggested_same_as_product_id,omitempty"`
	SameAsName   string  `json:"suggested_same_as_name,omitempty"`
	SimilarScore float64 `json:"similarity,omitempty"`
	SeenOn       int     `json:"receipt_lines"`
	ExampleLine  string  `json:"example_receipt_text,omitempty"`
}

type ReviewOut struct {
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
	Items  []ReviewCard   `json:"items"`
}

func (s *server) reviewNewProducts(ctx context.Context, _ *mcp.CallToolRequest, in ReviewIn) (*mcp.CallToolResult, ReviewOut, error) {
	limit := 20
	if in.Limit > 0 {
		limit = min(in.Limit, 100)
	}
	q, err := s.Auto.ReviewQueue(ctx, limit, 0, in.Reason)
	if err != nil {
		return nil, ReviewOut{}, err
	}
	out := ReviewOut{Total: q.Total, Counts: q.Counts, Items: []ReviewCard{}}
	for _, it := range q.Items {
		c := ReviewCard{ProductID: it.Product.ID, Name: it.Product.Name, NameEN: it.Product.NameEn.String, Reason: it.Reason,
			CreatedVia: it.Product.CreatedVia, Barcode: it.Product.Barcode.String, SeenOn: it.SourceLineCount}
		if it.SuggestedMerge != nil {
			c.SameAsID, c.SameAsName, c.SimilarScore = it.SuggestedMerge.ProductID, it.SuggestedMerge.Name, it.SuggestedMerge.Score
		}
		if len(it.SourceLines) > 0 {
			c.ExampleLine = it.SourceLines[0].RawText
		}
		out.Items = append(out.Items, c)
	}
	return nil, out, nil
}

type ResolveIn struct {
	ProductID     int64  `json:"product_id"`
	Action        string `json:"action" jsonschema:"accept, merge, keep_separate or discard"`
	IntoProductID int64  `json:"into_product_id,omitempty" jsonschema:"merge only; default: the suggested product"`
}

type ResolveOut struct {
	OK       bool   `json:"ok"`
	Action   string `json:"action"`
	Product  string `json:"product"`
	UndoID   int64  `json:"undo_id,omitempty" jsonschema:"merge/discard can be undone for 30 days in the app"`
	MergedIn string `json:"merged_away,omitempty"`
}

func (s *server) resolveReview(ctx context.Context, _ *mcp.CallToolRequest, in ResolveIn) (*mcp.CallToolResult, ResolveOut, error) {
	ri := autoprod.ReviewInput{Action: in.Action}
	if in.IntoProductID != 0 {
		ri.IntoProductID = &in.IntoProductID
	}
	res, err := s.Auto.Review(ctx, in.ProductID, ri)
	if err != nil {
		return nil, ResolveOut{}, err
	}
	out := ResolveOut{OK: true, Action: in.Action, Product: res.Product.Name}
	if res.Merge != nil {
		out.UndoID, out.MergedIn = res.Merge.MergeID, res.Merge.MergedAway.Name
	}
	if res.Discard != nil {
		out.UndoID = res.Discard.MergeID
	}
	return nil, out, nil
}
