package httpapi

import (
	"net/http"
	"time"

	"shop/internal/apitypes"
	"shop/internal/english"
	"shop/internal/translate"
)

func toSpans(in []translate.Span) []apitypes.Span {
	out := make([]apitypes.Span, len(in))
	for i, s := range in {
		out[i] = apitypes.Span{Word: s.Word, Key: s.Key, Start: s.Start, End: s.End}
	}
	return out
}

func toTerm(t english.Term) apitypes.GlossaryTerm {
	out := apitypes.GlossaryTerm{Dutch: t.Dutch, KeepAsIs: t.KeepAsIs, ProductsCount: t.ProductsCount, UpdatedAt: t.UpdatedAt}
	if t.English != "" {
		e := t.English
		out.English = &e
	}
	return out
}

func termInput(in apitypes.GlossaryTermInput) english.TermInput {
	out := english.TermInput{}
	if in.English != nil {
		out.English = *in.English
	}
	if in.KeepAsIs != nil {
		out.KeepAsIs = *in.KeepAsIs
	}
	return out
}

func (s *Server) listGlossary(w http.ResponseWriter, r *http.Request) {
	terms, err := s.English.Terms(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]apitypes.GlossaryTerm, len(terms))
	for i, t := range terms {
		out[i] = toTerm(t)
	}
	writeJSON(w, http.StatusOK, out)
}

type previewRequest struct {
	Dutch    string  `json:"dutch"`
	English  *string `json:"english"`
	KeepAsIs *bool   `json:"keep_as_is"`
	Examples *int    `json:"examples"`
}

func (s *Server) previewGlossary(w http.ResponseWriter, r *http.Request) {
	var in previewRequest
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	n := 3
	if in.Examples != nil {
		n = max(0, min(*in.Examples, 20))
	}
	affected, ex, err := s.English.Preview(r.Context(), in.Dutch,
		termInput(apitypes.GlossaryTermInput{English: in.English, KeepAsIs: in.KeepAsIs}), n)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	type example struct {
		ProductID    int64   `json:"product_id"`
		Name         string  `json:"name"`
		NameENBefore *string `json:"name_en_before,omitempty"`
		NameENAfter  *string `json:"name_en_after,omitempty"`
		StatusAfter  string  `json:"status_after"`
	}
	out := struct {
		ProductsAffected int       `json:"products_affected"`
		Examples         []example `json:"examples"`
	}{ProductsAffected: affected, Examples: []example{}}
	for _, e := range ex {
		out.Examples = append(out.Examples, example(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) teachWord(w http.ResponseWriter, r *http.Request) {
	var in apitypes.GlossaryTermInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	term, prev, n, err := s.English.Teach(r.Context(), r.PathValue("dutch"), termInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := map[string]any{"term": toTerm(term), "previous": nil, "products_updated": n}
	if prev != nil {
		p := apitypes.GlossaryTermInput{KeepAsIs: &prev.KeepAsIs}
		if prev.English != "" {
			p.English = &prev.English
		}
		out["previous"] = p
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) forgetWord(w http.ResponseWriter, r *http.Request) {
	n, err := s.English.Forget(r.Context(), r.PathValue("dutch"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"products_updated": n})
}

func (s *Server) translationTodo(w http.ResponseWriter, r *http.Request) {
	limit, err := s.intParam(r, "limit", 50, 1, 500)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	todo, err := s.English.Todo(r.Context(), limit, time.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	type word struct {
		Word     string   `json:"word"`
		Key      string   `json:"key"`
		Products int      `json:"products"`
		Lines    int      `json:"lines"`
		Examples []string `json:"examples"`
	}
	type product struct {
		ID            int64           `json:"id"`
		Name          string          `json:"name"`
		NameEN        *string         `json:"name_en,omitempty"`
		NameENStatus  string          `json:"name_en_status"`
		NameENUnknown []apitypes.Span `json:"name_en_unknown"`
		ShopName      *string         `json:"shop_name,omitempty"`
		ShopChain     *string         `json:"shop_chain,omitempty"`
	}
	out := struct {
		Totals   map[string]int `json:"totals"`
		Words    []word         `json:"words"`
		Products []product      `json:"products"`
	}{Totals: map[string]int{"words_left": todo.WordsLeft, "products_left": todo.ProductsLeft}, Words: []word{}, Products: []product{}}
	for _, wd := range todo.Words {
		out.Words = append(out.Words, word(wd))
	}
	for _, p := range todo.Products {
		out.Products = append(out.Products, product{ID: p.ID, Name: p.Name, NameEN: p.NameEN, NameENStatus: p.Status.Status,
			NameENUnknown: toSpans(p.Status.Unknown), ShopName: p.ShopName, ShopChain: p.ShopChain})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) previewTranslation(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" {
		s.writeErr(w, r, badf("text is required"))
		return
	}
	res := translate.Translate(text)
	out := map[string]any{"status": res.Status()}
	if res.OK() {
		out["english"] = res.Text
		out["unknown"] = toSpans(res.Unknown)
	} else {
		out["unknown"] = toSpans(res.UnknownInput)
	}
	writeJSON(w, http.StatusOK, out)
}
