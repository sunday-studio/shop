package httpapi

import (
	"database/sql"
	"io"
	"net/http"
	"strconv"

	"shop/internal/apitypes"
	"shop/internal/english"
	"shop/internal/match"
	"shop/internal/openfoodfacts"
	"shop/internal/products"
	"shop/internal/store"
	"shop/internal/store/db"
)

// The database stores nutrition source as 'off'; the API spells it out.
const dbSourceOpenFoodFacts = "off"

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

func toNutrition(n db.Nutrition) apitypes.Nutrition {
	per := apitypes.NutritionPer(n.Per)
	src := apitypes.NutritionSourceManual
	if n.Source == dbSourceOpenFoodFacts {
		src = apitypes.NutritionSourceOpenfoodfacts
	}
	return apitypes.Nutrition{
		Per: &per, Source: &src, EnergyKcal: floatPtr(n.EnergyKcal), Fat: floatPtr(n.Fat),
		SaturatedFat: floatPtr(n.SaturatedFat), Carbs: floatPtr(n.Carbs), Sugars: floatPtr(n.Sugars),
		Fibre: floatPtr(n.Fibre), Protein: floatPtr(n.Protein), Salt: floatPtr(n.Salt),
	}
}

func fromNutrition(n *apitypes.Nutrition) *products.Nutrition {
	if n == nil {
		return nil
	}
	out := &products.Nutrition{
		EnergyKcal: n.EnergyKcal, Fat: n.Fat, SaturatedFat: n.SaturatedFat, Carbs: n.Carbs,
		Sugars: n.Sugars, Fibre: n.Fibre, Protein: n.Protein, Salt: n.Salt,
	}
	if n.Per != nil {
		out.Per = string(*n.Per)
	}
	if n.Source != nil {
		out.Source = "manual"
		if *n.Source == apitypes.NutritionSourceOpenfoodfacts {
			out.Source = dbSourceOpenFoodFacts
		}
	}
	return out
}

func toProduct(p db.Product, n *db.Nutrition) apitypes.Product {
	created, _ := store.ParseTS(p.CreatedAt)
	updated, _ := store.ParseTS(p.UpdatedAt)
	out := apitypes.Product{
		Id: p.ID, Name: p.Name, NameEn: store.StrPtr(p.NameEn), Brand: store.StrPtr(p.Brand), CategoryId: store.IntPtr(p.CategoryID),
		Barcode: store.StrPtr(p.Barcode), NetQuantity: floatPtr(p.NetQuantity),
		OpenfoodfactsCode: store.StrPtr(p.OffCode), HasImage: p.ImagePath.Valid,
		CreatedAt: created, UpdatedAt: updated,
	}
	via := apitypes.ProductCreatedVia(p.CreatedVia)
	review := p.NeedsReview != 0
	out.CreatedVia, out.NeedsReview = &via, &review
	if p.ReviewReason.Valid {
		rr := apitypes.ReviewReason(p.ReviewReason.String)
		out.ReviewReason = &rr
	}
	if p.CategorySource.Valid {
		cs := apitypes.ProductCategorySource(p.CategorySource.String)
		out.CategorySource = &cs
	}
	if p.ImagePath.Valid {
		u := "/v1/products/" + itoa(p.ID) + "/image"
		out.ImageUrl = &u
	}
	if p.NetUnit.Valid {
		u := apitypes.ProductNetUnit(p.NetUnit.String)
		out.NetUnit = &u
	}
	if p.NameEnSource.Valid {
		src := apitypes.ProductNameEnSource(p.NameEnSource.String)
		out.NameEnSource = &src
	}
	st := english.ProductStatus(p)
	status := apitypes.NameEnStatus(st.Status)
	spans := toSpans(st.Unknown)
	out.NameEnStatus, out.NameEnUnknown = &status, &spans
	if n != nil {
		nu := toNutrition(*n)
		out.Nutrition = &nu
	}
	return out
}

func toMatches(cs []match.Candidate) *[]apitypes.ProductMatch {
	out := make([]apitypes.ProductMatch, len(cs))
	for i, c := range cs {
		out[i] = apitypes.ProductMatch{ProductId: c.ProductID, Name: c.Name, Score: round3(c.Score)}
		if c.NameEN != "" {
			en := c.NameEN
			out[i].NameEn = &en
		}
		if c.Brand != "" {
			b := c.Brand
			out[i].Brand = &b
		}
	}
	return &out
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

func productInput(in apitypes.ProductInput) products.Input {
	out := products.Input{
		Name: in.Name, NameEN: in.NameEn, Brand: in.Brand, CategoryID: in.CategoryId, Barcode: in.Barcode,
		NetQuantity: in.NetQuantity, OFFCode: in.OpenfoodfactsCode, ImageURL: in.ImageUrl,
		Nutrition: fromNutrition(in.Nutrition),
	}
	if in.NetUnit != nil {
		u := string(*in.NetUnit)
		out.NetUnit = &u
	}
	if in.NameEnSource != nil {
		out.NameENSource = string(*in.NameEnSource)
	}
	return out
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := 50, 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			s.writeErr(w, r, badf("limit must be 1..200"))
			return
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			s.writeErr(w, r, badf("invalid offset"))
			return
		}
		offset = n
	}
	var cat *int64
	if v := q.Get("category_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.writeErr(w, r, badf("invalid category_id"))
			return
		}
		cat = &n
	}
	res, err := s.Products.List(r.Context(), q.Get("q"), cat, limit, offset, q.Get("sort"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]apitypes.Product, len(res))
	for i, p := range res {
		out[i] = toProduct(p.Product, nil)
		if q.Get("q") != "" {
			sc := round3(p.Score)
			out[i].Score = &sc
		}
	}
	if err := s.withSummaries(r, out); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProduct(w http.ResponseWriter, r *http.Request) {
	var in apitypes.ProductInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.Products.Create(r.Context(), productInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProduct(p.Product, p.Nutrition))
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.Products.Get(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := []apitypes.Product{toProduct(p.Product, p.Nutrition)}
	out[0].SuggestedMerge = s.suggestedMergeOf(r.Context(), p.Product)
	if err := s.withSummaries(r, out); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out[0])
}

// withSummaries adds cheapest shop, last purchase and purchase count.
func (s *Server) withSummaries(r *http.Request, ps []apitypes.Product) error {
	if s.Stats == nil || len(ps) == 0 {
		return nil
	}
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.Id
	}
	sums, err := s.Stats.ProductSummaries(r.Context(), ids)
	if err != nil {
		return err
	}
	for i := range ps {
		sm := sums[ps[i].Id]
		n := sm.Purchases
		ps[i].Purchases, ps[i].LastBoughtAt = &n, sm.LastBoughtAt
		if c := sm.Cheapest; c != nil {
			ps[i].Cheapest = &apitypes.ShopPrice{
				ShopId: c.ShopID, ShopName: c.ShopName, BaseUnit: apitypes.ShopPriceBaseUnit(c.BaseUnit),
				LatestUnitCents: c.Latest, LatestAt: c.LatestAt, MinUnitCents: c.Min, MaxUnitCents: c.Max,
				Purchases: c.Purchases, Cheapest: c.Cheapest,
			}
		}
	}
	return nil
}

func (s *Server) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.ProductInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if in.ImageUrl != nil {
		s.writeErr(w, r, badf("image_url is only accepted on create; use PUT /image"))
		return
	}
	p, err := s.Products.Update(r.Context(), id, productInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProduct(p.Product, p.Nutrition))
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Products.Delete(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) productImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.Products.Get(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	path := s.Products.ImagePath(p)
	if path == "" {
		writeProblem(w, http.StatusNotFound, "no image", "")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) putProductImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	f, _, err := r.FormFile("image")
	if err != nil {
		s.writeErr(w, r, badf("image file is required"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpload))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.Products.SetImage(r.Context(), id, data)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProduct(p.Product, p.Nutrition))
}

func (s *Server) putNutrition(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.Nutrition
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.Products.SetNutrition(r.Context(), id, fromNutrition(&in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProduct(p.Product, p.Nutrition))
}

func (s *Server) deleteNutrition(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if _, err := s.Products.SetNutrition(r.Context(), id, nil); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) lookupBarcode(w http.ResponseWriter, r *http.Request) {
	res, err := s.Products.Barcode(r.Context(), r.PathValue("code"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if res.Local != nil {
		p := toProduct(res.Local.Product, res.Local.Nutrition)
		writeJSON(w, http.StatusOK, apitypes.BarcodeLookup{Source: apitypes.BarcodeLookupSourceLocal, Product: &p})
		return
	}
	writeJSON(w, http.StatusOK, apitypes.BarcodeLookup{Source: apitypes.BarcodeLookupSourceOpenfoodfacts, Prefill: toPrefill(*res.Prefill)})
}

func toPrefill(p openfoodfacts.Product) *apitypes.OpenFoodFactsPrefill {
	out := &apitypes.OpenFoodFactsPrefill{Code: p.Code, Name: p.Name, NetQuantity: p.NetQuantity}
	if p.Brand != "" {
		out.Brand = &p.Brand
	}
	if p.NameEN != "" {
		out.NameEn = &p.NameEN
	}
	if p.NetUnit != "" {
		u := apitypes.OpenFoodFactsPrefillNetUnit(p.NetUnit)
		out.NetUnit = &u
	}
	if p.ImageURL != "" {
		out.ImageUrl = &p.ImageURL
	}
	if n := p.Nutrition; n != nil {
		per := apitypes.NutritionPer(n.Per)
		src := apitypes.NutritionSourceOpenfoodfacts
		out.Nutrition = &apitypes.Nutrition{
			Per: &per, Source: &src, EnergyKcal: n.EnergyKcal, Fat: n.Fat, SaturatedFat: n.SaturatedFat,
			Carbs: n.Carbs, Sugars: n.Sugars, Fibre: n.Fibre, Protein: n.Protein, Salt: n.Salt,
		}
	}
	return out
}

func (s *Server) listAliases(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	rows, err := s.Products.Aliases(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]apitypes.Alias, len(rows))
	for i, a := range rows {
		created, _ := store.ParseTS(a.CreatedAt)
		out[i] = apitypes.Alias{
			Id: a.ID, Text: a.NormText, ShopId: store.IntPtr(a.ShopID), ShopName: store.StrPtr(a.ShopName),
			ProductId: a.ProductID, CreatedAt: created,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteAlias(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Products.DeleteAlias(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unmatchedLines(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	days := 14
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			s.writeErr(w, r, badf("days must be 1..365"))
			return
		}
		days = n
	}
	rows, err := s.Products.UnmatchedLinesFor(r.Context(), id, days)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]apitypes.UnmatchedLine, len(rows))
	for i, u := range rows {
		out[i] = unmatchedLineOut(u)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) linkLine(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.LinkInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	remember := in.Remember == nil || *in.Remember
	res, err := s.Products.LinkLine(r.Context(), id, in.ProductId, remember)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.LinkResult{
		Line: toLine(res.Line), AlsoLinked: res.AlsoLinked, AliasCreated: res.AliasCreated,
	})
}

func unmatchedLineOut(u products.UnmatchedLine) apitypes.UnmatchedLine {
	return apitypes.UnmatchedLine{
		Line: toLine(db.ReceiptLine{
			ID: u.ID, ReceiptID: u.ReceiptID, LineNo: u.LineNo, RawText: u.RawText, NormText: u.NormText,
			Kind: u.Kind, Qty: u.Qty, Unit: u.Unit, UnitPriceCents: u.UnitPriceCents,
			LineTotalCents: u.LineTotalCents, VatCode: u.VatCode, ParentLineID: u.ParentLineID,
			ProductID: u.ProductID, UserEdited: u.UserEdited,
		}),
		ShopId: store.IntPtr(u.ShopID), ShopName: u.ShopName, PurchasedAt: store.TimePtr(u.PurchasedAt),
		Score: round3(u.Score),
	}
}

func (s *Server) restoreProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.Products.Restore(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.productFull(r.Context(), p.Product))
}
