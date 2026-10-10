package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"shop/internal/apitypes"
	"shop/internal/autoprod"
	"shop/internal/openfoodfacts"
	"shop/internal/store"
	"shop/internal/store/db"
)

func (s *Server) auto(w http.ResponseWriter, r *http.Request) *autoprod.Service {
	if s.Auto == nil {
		writeProblem(w, http.StatusServiceUnavailable, "not available", "automatic products are not configured")
	}
	return s.Auto
}

func toSuggestedMerge(m *autoprod.SuggestedMerge) *apitypes.SuggestedMerge {
	if m == nil {
		return nil
	}
	out := &apitypes.SuggestedMerge{ProductId: m.ProductID, Name: m.Name, Score: round3(m.Score)}
	if m.NameEN != "" {
		out.NameEn = &m.NameEN
	}
	if m.Brand != "" {
		out.Brand = &m.Brand
	}
	return out
}

// suggestedMergeOf resolves a product's suggested_merge_id into the object the API shows.
func (s *Server) suggestedMergeOf(ctx context.Context, p db.Product) *apitypes.SuggestedMerge {
	if !p.SuggestedMergeID.Valid {
		return nil
	}
	sp, err := s.q.GetProduct(ctx, p.SuggestedMergeID.Int64)
	if err != nil {
		return nil
	}
	return toSuggestedMerge(&autoprod.SuggestedMerge{ProductID: sp.ID, Name: sp.Name, NameEN: sp.NameEn.String, Brand: sp.Brand.String,
		Score: autoprod.Similarity(p.Name, sp.Name)})
}

// productFull maps a product row with its nutrition and suggested merge.
func (s *Server) productFull(ctx context.Context, p db.Product) apitypes.Product {
	var n *db.Nutrition
	if nu, err := s.q.GetNutrition(ctx, p.ID); err == nil {
		n = &nu
	}
	out := toProduct(p, n)
	out.SuggestedMerge = s.suggestedMergeOf(ctx, p)
	return out
}

func (s *Server) getReviewQueue(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	q := r.URL.Query()
	limit, offset := 50, 0
	var err error
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 0 || limit > 200 {
			s.writeErr(w, r, badf("limit must be 0-200"))
			return
		}
	}
	if v := q.Get("offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 {
			s.writeErr(w, r, badf("offset must be >= 0"))
			return
		}
	}
	res, err := a.ReviewQueue(r.Context(), limit, offset, q.Get("reason"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	items := make([]apitypes.ReviewItem, len(res.Items))
	for i, it := range res.Items {
		lines := make([]apitypes.ReviewSourceLine, len(it.SourceLines))
		for j, l := range it.SourceLines {
			lines[j] = apitypes.ReviewSourceLine{LineId: l.LineID, ReceiptId: l.ReceiptID, RawText: l.RawText,
				ShopName: l.ShopName, LineTotalCents: l.LineTotalCents}
			if t, err := store.ParseTS(l.PurchasedAt); err == nil {
				lines[j].PurchasedAt = &t
			}
		}
		prod := s.productFull(r.Context(), it.Product)
		items[i] = apitypes.ReviewItem{Product: prod, Reason: apitypes.ReviewReason(it.Reason), SuggestedMerge: toSuggestedMerge(it.SuggestedMerge),
			SourceLineCount: it.SourceLineCount, SourceLines: lines}
	}
	writeJSON(w, http.StatusOK, apitypes.ReviewQueue{Total: res.Total, Counts: res.Counts, Items: items})
}

func (s *Server) reviewProduct(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.ReviewInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := a.Review(r.Context(), id, autoprod.ReviewInput{Action: string(in.Action), CategoryID: in.CategoryId, Name: in.Name, IntoProductID: in.IntoProductId})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := apitypes.ReviewResult{Product: s.productFull(r.Context(), res.Product)}
	switch {
	case res.Merge != nil:
		kind := apitypes.ReviewResultKindMerge
		out.Kind, out.MergeId = &kind, &res.Merge.MergeID
		out.MergedAway = &apitypes.ProductRef{Id: res.Merge.MergedAway.ID, Name: res.Merge.MergedAway.Name}
		out.LinesMoved, out.AliasesMoved = &res.Merge.LinesMoved, &res.Merge.AliasesMoved
	case res.Discard != nil:
		kind := apitypes.ReviewResultKindDiscard
		out.Kind, out.DiscardId = &kind, &res.Discard.MergeID
		out.LinesUnlinked = &res.Discard.LinesMoved
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) mergeProduct(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		IntoProductID int64 `json:"into_product_id"`
	}
	if err := decode(r, &in); err != nil || in.IntoProductID == 0 {
		s.writeErr(w, r, badf("into_product_id is required"))
		return
	}
	m, err := a.Merge(r.Context(), id, in.IntoProductID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.MergeResult{MergeId: m.MergeID, Kind: apitypes.MergeResultKindMerge, Product: s.productFull(r.Context(), m.Product),
		MergedAway: apitypes.ProductRef{Id: m.MergedAway.ID, Name: m.MergedAway.Name}, LinesMoved: m.LinesMoved, AliasesMoved: m.AliasesMoved})
}

func (s *Server) refreshProduct(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, status, err := a.Refresh(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": s.productFull(r.Context(), p), "off_status": status})
}

func (s *Server) undoProductMerge(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := a.Undo(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.UndoResult{Kind: apitypes.UndoResultKind(u.Kind), Product: s.productFull(r.Context(), u.Product),
		Into: s.productFull(r.Context(), u.Into), LinesRestored: u.LinesRestored, LinesSkipped: u.LinesSkipped})
}

func (s *Server) splitLineProduct(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16)); len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			s.writeErr(w, r, badf("invalid JSON body"))
			return
		}
	}
	m, err := a.Split(r.Context(), id, in.Name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.SplitResult{MergeId: m.MergeID, Kind: apitypes.SplitResultKindSplit, Product: s.productFull(r.Context(), m.Product), LinesMoved: m.LinesMoved})
}

func (s *Server) createBarcodeProduct(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	if !openfoodfacts.ValidBarcode(r.PathValue("code")) {
		s.writeErr(w, r, badf("barcode must be 8 or 12-14 digits"))
		return
	}
	o, err := a.CreateFromBarcode(r.Context(), r.PathValue("code"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	lines := make([]apitypes.UnmatchedLine, 0, len(o.Unmatched))
	for _, u := range o.Unmatched {
		lines = append(lines, unmatchedLineOut(u))
	}
	out := apitypes.BarcodeCreateResult{
		Status: apitypes.BarcodeCreateResultStatus(o.Status), CreatedNow: o.CreatedNow,
		Product: s.productFull(r.Context(), o.Product), MergeSuggestion: toSuggestedMerge(o.MergeSuggestion), UnmatchedLines: lines,
	}
	if o.OFFStatus != "" {
		st := apitypes.BarcodeCreateResultOffStatus(o.OFFStatus)
		out.OffStatus = &st
	}
	code := http.StatusOK
	if o.CreatedNow {
		code = http.StatusCreated
	}
	// explicit nulls for the app: merge_suggestion is always present
	b, _ := json.Marshal(out)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	if out.MergeSuggestion == nil {
		m["merge_suggestion"] = json.RawMessage("null")
	}
	writeJSON(w, code, m)
}

func (s *Server) getBackfill(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	st, err := a.GetBackfill(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) startBackfill(w http.ResponseWriter, r *http.Request) {
	a := s.auto(w, r)
	if a == nil {
		return
	}
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	if body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16)); len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			s.writeErr(w, r, badf("invalid JSON body"))
			return
		}
	}
	id, err := a.StartBackfill(r.Context(), in.DryRun)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"job_id": id})
}
