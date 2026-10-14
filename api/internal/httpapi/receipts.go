package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"shop/internal/apitypes"
	"shop/internal/receipts"
	"shop/internal/store"
	"shop/internal/store/db"
)

const maxUpload = 20 << 20

func (s *Server) toReceipt(r db.Receipt, lines []db.ReceiptLine) apitypes.Receipt {
	created, _ := store.ParseTS(r.CreatedAt)
	updated, _ := store.ParseTS(r.UpdatedAt)
	reasons := []apitypes.ReceiptReviewReasons{}
	if r.ReviewReasons.Valid {
		_ = json.Unmarshal([]byte(r.ReviewReasons.String), &reasons)
	}
	out := apitypes.Receipt{
		Id: r.ID, ShopId: store.IntPtr(r.ShopID), PhotoTakenAt: store.TimePtr(r.PhotoTakenAt), PurchasedAt: store.TimePtr(r.PurchasedAt),
		TotalCents: store.IntPtr(r.TotalCents), ParsedSumCents: store.IntPtr(r.ParsedSumCents),
		PaymentMethod: store.StrPtr(r.PaymentMethod), Status: apitypes.ReceiptStatus(r.Status),
		ReviewReasons: reasons, OcrEngine: store.StrPtr(r.OcrEngine), OcrMs: store.IntPtr(r.OcrMs),
		ConfirmedAt: store.TimePtr(r.ConfirmedAt), CreatedAt: created, UpdatedAt: updated,
	}
	if r.ParserVersion.Valid {
		v := int(r.ParserVersion.Int64)
		out.ParserVersion = &v
	}
	if lines != nil {
		ls := make([]apitypes.ReceiptLine, len(lines))
		var items, unlinked int
		for i, l := range lines {
			ls[i] = s.toLine(l)
			if l.Kind == "item" {
				items++
				if !l.ProductID.Valid {
					unlinked++
				}
			}
		}
		out.Lines = &ls
		out.LineCount, out.UnlinkedCount = &items, &unlinked
	}
	return out
}

// withProducts adds the linked product's names and category to lines.
func (s *Server) withProducts(r *http.Request, lines []apitypes.ReceiptLine) error {
	var ids []int64
	for _, l := range lines {
		if l.ProductId != nil {
			ids = append(ids, *l.ProductId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ps, err := s.q.GetProductsByIDs(r.Context(), ids)
	if err != nil {
		return err
	}
	byID := make(map[int64]db.Product, len(ps))
	for _, p := range ps {
		byID[p.ID] = p
	}
	for i, l := range lines {
		if l.ProductId == nil {
			continue
		}
		if p, ok := byID[*l.ProductId]; ok {
			name := p.Name
			lines[i].ProductName, lines[i].ProductNameEn, lines[i].CategoryId = &name, store.StrPtr(p.NameEn), store.IntPtr(p.CategoryID)
		}
	}
	return nil
}

func (s *Server) receiptCounts(w http.ResponseWriter, r *http.Request) {
	c, err := s.Receipts.Counts(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	dups, err := s.Receipts.PossibleDuplicates(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	c["possible_duplicates"] = int64(len(dups))
	writeJSON(w, http.StatusOK, c)
}

// toPossibleDuplicate adds the older receipt's shop to a detected duplicate.
func (s *Server) toPossibleDuplicate(r *http.Request, d *receipts.Duplicate) (*apitypes.PossibleDuplicate, error) {
	if d == nil {
		return nil, nil
	}
	at := d.PurchasedAt
	out := &apitypes.PossibleDuplicate{
		ReceiptId: d.ReceiptID, PurchasedAt: &at, TotalCents: d.TotalCents, Match: apitypes.PossibleDuplicateMatch(d.Match),
		SharedLines: d.SharedLines, ItemCount: d.ItemLines, OtherItemCount: d.OtherItemLines, TimeMatches: d.TimeMatches,
	}
	if d.ShopID != nil {
		sh, err := s.q.GetShop(r.Context(), *d.ShopID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			out.Shop = &apitypes.ShopRef{Id: sh.ID, Name: sh.Name, Chain: store.StrPtr(sh.Chain)}
			out.ShopName = &sh.Name
		}
	}
	return out, nil
}

func (s *Server) keepDuplicate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Receipts.DismissDuplicate(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) undoKeepDuplicate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Receipts.UndismissDuplicate(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) toLine(l db.ReceiptLine) apitypes.ReceiptLine {
	out := apitypes.ReceiptLine{
		Id: l.ID, ReceiptId: l.ReceiptID, LineNo: int(l.LineNo), RawText: l.RawText,
		Kind: apitypes.LineKind(l.Kind), Qty: l.Qty, Unit: apitypes.LineUnit(l.Unit),
		UnitPriceCents: store.IntPtr(l.UnitPriceCents), LineTotalCents: l.LineTotalCents,
		VatCode: store.StrPtr(l.VatCode), ParentLineId: store.IntPtr(l.ParentLineID),
		ProductId: store.IntPtr(l.ProductID), UserEdited: l.UserEdited != 0,
	}
	if by := receipts.MatchedBy(l); by != "" {
		m := apitypes.ReceiptLineMatchedBy(by)
		out.MatchedBy = &m
		if l.MatchScore.Valid {
			sc := l.MatchScore.Float64
			out.MatchScore = &sc
		}
	}
	if l.Kind == "item" {
		g := s.English.Gloss(l.RawText)
		status := apitypes.ReceiptLineTextEnStatus(g.Status)
		spans := toSpans(g.Unknown)
		out.TextEn, out.TextEnStatus, out.TextEnUnknown = g.TextEN, &status, &spans
	}
	return out
}

func (s *Server) listReceipts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := receipts.ListFilter{Limit: 50}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 200 {
			s.writeErr(w, r, badf("limit must be 1..200"))
			return
		}
		f.Limit = n
	}
	if v := q.Get("shop_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.writeErr(w, r, badf("invalid shop_id"))
			return
		}
		f.ShopID = &n
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.writeErr(w, r, badf("invalid cursor"))
			return
		}
		f.BeforeID = &n
	}
	var err error
	if f.From, err = s.localDate(q.Get("from"), 0); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if f.To, err = s.localDate(q.Get("to"), 1); err != nil { // inclusive end date
		s.writeErr(w, r, err)
		return
	}

	recs, err := s.Receipts.List(r.Context(), f)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	dups, err := s.Receipts.PossibleDuplicates(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	page := apitypes.ReceiptPage{Items: make([]apitypes.Receipt, len(recs))}
	for i, rec := range recs {
		page.Items[i] = s.toReceipt(rec.Receipt, nil)
		if page.Items[i].PossibleDuplicateOf, err = s.toPossibleDuplicate(r, dups[rec.Receipt.ID]); err != nil {
			s.writeErr(w, r, err)
			return
		}
		lc, uc := int(rec.LineCount), int(rec.UnlinkedCount)
		page.Items[i].LineCount, page.Items[i].UnlinkedCount = &lc, &uc
	}
	if int64(len(recs)) == f.Limit {
		c := itoa(recs[len(recs)-1].Receipt.ID)
		page.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, page)
}

// localDate parses YYYY-MM-DD as local midnight, plus addDays.
func (s *Server) localDate(v string, addDays int) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation(time.DateOnly, v, s.Location)
	if err != nil {
		return nil, badf("invalid date %q, want YYYY-MM-DD", v)
	}
	t = t.AddDate(0, 0, addDays)
	return &t, nil
}

func (s *Server) uploadReceipt(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.writeErr(w, r, badf("invalid multipart form: %v", err))
		return
	}
	var shopID *int64
	if v := r.FormValue("shop_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.writeErr(w, r, badf("shop_id must be an integer"))
			return
		}
		shopID = &n
	}
	var photoTaken *time.Time
	if v := r.FormValue("photo_taken_at"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			s.writeErr(w, r, badf("photo_taken_at must be RFC3339"))
			return
		}
		photoTaken = &t
	}
	var purchased *time.Time
	if v := r.FormValue("purchased_at"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			s.writeErr(w, r, badf("purchased_at must be RFC3339"))
			return
		}
		purchased = &t
	}
	f, _, err := r.FormFile("image")
	if err != nil {
		s.writeErr(w, r, badf("image file is required"))
		return
	}
	defer f.Close()
	img, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if len(img) > maxUpload {
		writeProblem(w, http.StatusRequestEntityTooLarge, "image too large", "max 20 MB")
		return
	}

	finish := func(bool) {}
	if g := uploadGuardFrom(r.Context()); g != nil {
		f, ok := g(w, r, int64(len(img)))
		if !ok {
			return // the guard answered (file too large, quota reached, ...)
		}
		finish = f
	}
	rec, err := s.Receipts.Ingest(r.Context(), shopID, purchased, photoTaken, img)
	if err != nil {
		finish(false)
		s.writeErr(w, r, err)
		return
	}
	finish(true)
	w.Header().Set("Location", "/v1/receipts/"+itoa(rec.ID))
	writeJSON(w, http.StatusAccepted, s.toReceipt(rec, nil))
}

func (s *Server) getReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.receiptOut(r, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// receiptOut builds the single-receipt payload: lines with product names and link suggestions, and
// the shop suggestion.
func (s *Server) receiptOut(r *http.Request, id int64) (apitypes.Receipt, error) {
	rec, lines, err := s.Receipts.Get(r.Context(), id)
	if err != nil {
		return apitypes.Receipt{}, err
	}
	out := s.toReceipt(rec, lines)
	sug, err := s.suggest.Suggest(r.Context(), id)
	if err != nil {
		return apitypes.Receipt{}, err
	}
	out.ShopSuggestion = toShopSuggestion(sug)
	dup, err := s.Receipts.PossibleDuplicate(r.Context(), id)
	if err != nil {
		return apitypes.Receipt{}, err
	}
	if out.PossibleDuplicateOf, err = s.toPossibleDuplicate(r, dup); err != nil {
		return apitypes.Receipt{}, err
	}
	if err := s.withProducts(r, *out.Lines); err != nil {
		return apitypes.Receipt{}, err
	}
	for i, l := range *out.Lines {
		if l.Kind != apitypes.Item || l.ProductId != nil {
			continue
		}
		sugg, err := s.lineSuggestions(r, rec.ShopID.Int64, lines[i], l.RawText)
		if err != nil {
			return apitypes.Receipt{}, err
		}
		(*out.Lines)[i].Suggestions = sugg
	}
	return out, nil
}

// lineSuggestions returns the suggestions the scan stored on an unlinked item line, with the products'
// current names (a product deleted or merged away since is dropped). A line the scan never looked at
// (read before scan-time matching existed, or the matcher failed) is matched now.
func (s *Server) lineSuggestions(r *http.Request, shopID int64, l db.ReceiptLine, rawText string) (*[]apitypes.ProductMatch, error) {
	stored, ok := receipts.ParseSuggestions(l.Suggestions)
	if !ok {
		if s.Products == nil {
			return nil, nil
		}
		cands, err := s.Products.Suggest(r.Context(), shopID, rawText)
		if err != nil {
			return nil, err
		}
		return toMatches(cands), nil
	}
	out := []apitypes.ProductMatch{}
	if len(stored) == 0 {
		return &out, nil
	}
	ids := make([]int64, len(stored))
	for i, c := range stored {
		ids[i] = c.ProductID
	}
	ps, err := s.q.GetProductsByIDs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]db.Product, len(ps))
	for _, p := range ps {
		byID[p.ID] = p
	}
	for _, c := range stored {
		p, ok := byID[c.ProductID]
		if !ok {
			continue
		}
		m := apitypes.ProductMatch{ProductId: p.ID, Name: p.Name, Score: c.Score, NameEn: store.StrPtr(p.NameEn), Brand: store.StrPtr(p.Brand)}
		out = append(out, m)
	}
	return &out, nil
}

func (s *Server) updateReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.ReceiptPatch
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if _, err := s.Receipts.UpdateHeader(r.Context(), id, receipts.HeaderPatch{
		ShopID: in.ShopId, PurchasedAt: in.PurchasedAt, TotalCents: in.TotalCents,
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.getReceipt(w, r)
}

func (s *Server) deleteReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.revertList(r, id) // put the list back before the receipt (and its checks) go
	if err := s.Receipts.Delete(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) receiptImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	rec, _, err := s.Receipts.Get(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable") // content-addressed
	http.ServeFile(w, r, s.Receipts.ImagePath(rec))
}

func (s *Server) confirmReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if _, err := s.Receipts.Confirm(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	// Automatic products (flag AUTO_PRODUCTS): a failure here never fails the confirm.
	var summary *apitypes.AutoProductsSummary
	if s.Auto != nil && s.Auto.Enabled() {
		st, aerr := s.Auto.ProcessReceipt(r.Context(), id, "receipt")
		summary = &apitypes.AutoProductsSummary{Linked: st.Linked, Created: st.Created, NeedsReview: st.NeedsReview}
		if aerr != nil {
			s.Log.Warn("auto products on confirm", "receipt", id, "err", aerr)
			msg := aerr.Error()
			summary.Error = &msg
		}
	}
	// Shopping list (phase A): a failure here never fails the confirm either.
	listOut := s.reconcileList(r, id)
	out, err := s.receiptOut(r, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out.AutoProducts = summary
	out.List = listOut
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) requeue(withOCR bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		force := r.URL.Query().Get("force") == "true"
		rec, err := s.Receipts.Requeue(r.Context(), id, withOCR, force)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if force {
			s.revertList(r, id) // a forced re-parse unconfirms the receipt
		}
		writeJSON(w, http.StatusAccepted, s.toReceipt(rec, nil))
	}
}

// reconcileList checks off list items for a just-confirmed receipt. Errors and panics are logged only.
func (s *Server) reconcileList(r *http.Request, id int64) (out *apitypes.ReceiptListChecks) {
	if s.List == nil {
		return nil
	}
	defer func() {
		if p := recover(); p != nil {
			s.Log.Warn("list checks on confirm panicked", "receipt", id, "panic", p)
			out = nil
		}
	}()
	res, err := s.List.ReconcileReceipt(r.Context(), id)
	if err != nil {
		s.Log.Warn("list checks on confirm", "receipt", id, "err", err)
		return nil
	}
	// The list handlers build maps; the typed model is the same JSON.
	raw, _ := json.Marshal(map[string]any{
		"checked": toItems(res.Checked), "reduced": toItems(res.Reduced), "suggestions": toSuggestions(res.Suggestions),
	})
	var lc apitypes.ReceiptListChecks
	if err := json.Unmarshal(raw, &lc); err != nil {
		s.Log.Warn("list checks on confirm", "receipt", id, "err", err)
		return nil
	}
	return &lc
}

func (s *Server) revertList(r *http.Request, id int64) {
	if s.List == nil {
		return
	}
	if _, err := s.List.RevertReceipt(r.Context(), id); err != nil {
		s.Log.Warn("revert list checks", "receipt", id, "err", err)
	}
}

func lineInput(in apitypes.LineInput) receipts.LineInput {
	out := receipts.LineInput{
		RawText: in.RawText, Kind: string(in.Kind), LineTotalCents: in.LineTotalCents,
		UnitPriceCents: in.UnitPriceCents, ParentLineID: in.ParentLineId,
	}
	if in.Qty != nil {
		out.Qty = *in.Qty
	}
	if in.Unit != nil {
		out.Unit = string(*in.Unit)
	}
	if in.VatCode != nil {
		out.VATCode = *in.VatCode
	}
	return out
}

func (s *Server) createLine(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.LineInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	l, err := s.Receipts.CreateLine(r.Context(), id, lineInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toLine(l))
}

func (s *Server) updateLine(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.LineInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	l, err := s.Receipts.UpdateLine(r.Context(), id, lineInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toLine(l))
}

func (s *Server) deleteLine(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Receipts.DeleteLine(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
