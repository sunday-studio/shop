package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"shop/internal/apitypes"
	"shop/internal/english"
	"shop/internal/receipts"
	"shop/internal/store"
	"shop/internal/store/db"
)

const maxUpload = 20 << 20

func toReceipt(r db.Receipt, lines []db.ReceiptLine) apitypes.Receipt {
	created, _ := store.ParseTS(r.CreatedAt)
	updated, _ := store.ParseTS(r.UpdatedAt)
	reasons := []apitypes.ReceiptReviewReasons{}
	if r.ReviewReasons.Valid {
		_ = json.Unmarshal([]byte(r.ReviewReasons.String), &reasons)
	}
	out := apitypes.Receipt{
		Id: r.ID, ShopId: r.ShopID, PurchasedAt: store.TimePtr(r.PurchasedAt),
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
		for i, l := range lines {
			ls[i] = toLine(l)
		}
		out.Lines = &ls
	}
	return out
}

func toLine(l db.ReceiptLine) apitypes.ReceiptLine {
	out := apitypes.ReceiptLine{
		Id: l.ID, ReceiptId: l.ReceiptID, LineNo: int(l.LineNo), RawText: l.RawText,
		Kind: apitypes.LineKind(l.Kind), Qty: l.Qty, Unit: apitypes.LineUnit(l.Unit),
		UnitPriceCents: store.IntPtr(l.UnitPriceCents), LineTotalCents: l.LineTotalCents,
		VatCode: store.StrPtr(l.VatCode), ParentLineId: store.IntPtr(l.ParentLineID),
		ProductId: store.IntPtr(l.ProductID), UserEdited: l.UserEdited != 0,
	}
	if l.Kind == "item" {
		g := english.Gloss(l.RawText)
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
	page := apitypes.ReceiptPage{Items: make([]apitypes.Receipt, len(recs))}
	for i, rec := range recs {
		page.Items[i] = toReceipt(rec, nil)
	}
	if int64(len(recs)) == f.Limit {
		c := itoa(recs[len(recs)-1].ID)
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
	shopID, err := strconv.ParseInt(r.FormValue("shop_id"), 10, 64)
	if err != nil {
		s.writeErr(w, r, badf("shop_id is required"))
		return
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

	rec, err := s.Receipts.Ingest(r.Context(), shopID, purchased, img)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/receipts/"+itoa(rec.ID))
	writeJSON(w, http.StatusAccepted, toReceipt(rec, nil))
}

func (s *Server) getReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	rec, lines, err := s.Receipts.Get(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := toReceipt(rec, lines)
	if s.Products != nil {
		for i, l := range *out.Lines {
			if l.Kind != apitypes.Item || l.ProductId != nil {
				continue
			}
			cands, err := s.Products.Suggest(r.Context(), rec.ShopID, l.RawText)
			if err != nil {
				s.writeErr(w, r, err)
				return
			}
			(*out.Lines)[i].Suggestions = toMatches(cands)
		}
	}
	writeJSON(w, http.StatusOK, out)
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
	s.getReceipt(w, r)
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
		writeJSON(w, http.StatusAccepted, toReceipt(rec, nil))
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
	writeJSON(w, http.StatusCreated, toLine(l))
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
	writeJSON(w, http.StatusOK, toLine(l))
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
