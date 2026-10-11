package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shop/internal/apitypes"
	"shop/internal/categories"
	"shop/internal/logos"
	"shop/internal/store"
	"shop/internal/store/db"
)

func (s *Server) toShop(sh db.Shop) apitypes.Shop {
	created, _ := store.ParseTS(sh.CreatedAt)
	updated, _ := store.ParseTS(sh.UpdatedAt)
	out := apitypes.Shop{
		Id: sh.ID, Name: sh.Name, Chain: store.StrPtr(sh.Chain), Branch: store.StrPtr(sh.Branch),
		Address: store.StrPtr(sh.Address), City: store.StrPtr(sh.City), Notes: store.StrPtr(sh.Notes),
		Domain: store.StrPtr(sh.Domain), CreatedAt: created, UpdatedAt: updated,
	}
	if sh.Domain.Valid && s.Logos.Enabled() {
		u := "/v1/shops/" + itoa(sh.ID) + "/logo"
		out.LogoUrl = &u
	}
	return out
}

// shopDomain decides the stored domain: an explicit value (normalised; ""
// clears it), else the current one, else a guess from the chain/name.
func shopDomain(in apitypes.ShopInput, cur sql.NullString) (sql.NullString, error) {
	if in.Domain != nil {
		d, err := logos.NormaliseDomain(*in.Domain)
		if err != nil {
			return sql.NullString{}, badf("domain must look like ah.nl")
		}
		return store.Str(d), nil
	}
	if cur.Valid {
		return cur, nil
	}
	chain := ""
	if in.Chain != nil {
		chain = *in.Chain
	}
	return store.Str(logos.GuessDomain(in.Name, chain)), nil
}

func (s *Server) shopLogo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sh, err := s.q.GetShop(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if !sh.Domain.Valid || !s.Logos.Enabled() {
		writeProblem(w, http.StatusNotFound, "no logo", "")
		return
	}
	path, err := s.Logos.Path(r.Context(), sh.Domain.String)
	if errors.Is(err, logos.ErrNoLogo) || errors.Is(err, logos.ErrDomain) {
		writeProblem(w, http.StatusNotFound, "no logo", "")
		return
	}
	if err != nil {
		s.Log.Warn("logo fetch", "shop", id, "err", err)
		writeProblem(w, http.StatusBadGateway, "logo unavailable", "")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	http.ServeFile(w, r, path)
}

func decodeShop(r *http.Request) (apitypes.ShopInput, error) {
	var in apitypes.ShopInput
	if err := decode(r, &in); err != nil {
		return in, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, badf("name is required")
	}
	return in, nil
}

func (s *Server) listShops(w http.ResponseWriter, r *http.Request) {
	shops, err := s.q.ListShops(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]apitypes.Shop, len(shops))
	for i, sh := range shops {
		out[i] = s.toShop(sh)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getShop(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sh, err := s.q.GetShop(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toShop(sh))
}

func (s *Server) createShop(w http.ResponseWriter, r *http.Request) {
	in, err := decodeShop(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	domain, err := shopDomain(in, sql.NullString{})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := store.Now()
	sh, err := s.q.CreateShop(r.Context(), db.CreateShopParams{
		Name: in.Name, Chain: store.NullStr(in.Chain), Branch: store.NullStr(in.Branch),
		Address: store.NullStr(in.Address), City: store.NullStr(in.City), Notes: store.NullStr(in.Notes),
		Domain: domain, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toShop(sh))
}

func (s *Server) updateShop(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	in, err := decodeShop(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	cur, err := s.q.GetShop(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	domain, err := shopDomain(in, cur.Domain)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sh, err := s.q.UpdateShop(r.Context(), db.UpdateShopParams{
		Name: in.Name, Chain: store.NullStr(in.Chain), Branch: store.NullStr(in.Branch),
		Address: store.NullStr(in.Address), City: store.NullStr(in.City), Notes: store.NullStr(in.Notes),
		Domain: domain, UpdatedAt: store.Now(), ID: id,
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toShop(sh))
}

func (s *Server) deleteShop(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := store.TS(time.Now())
	n, err := s.q.SoftDeleteShop(r.Context(), db.SoftDeleteShopParams{DeletedAt: store.Str(now), UpdatedAt: now, ID: id})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if n == 0 {
		writeProblem(w, http.StatusNotFound, "not found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toCategory(c categories.Item) apitypes.Category {
	out := apitypes.Category{Id: c.ID, Name: c.Name, Sort: 0, ProductCount: int(c.ProductCount)}
	if c.Color.Valid {
		col := apitypes.CategoryColor(c.Color.String)
		out.Color = &col
	}
	return out
}

func decodeCategory(r *http.Request) (apitypes.CategoryInput, error) {
	var in apitypes.CategoryInput
	if err := decode(r, &in); err != nil {
		return in, err
	}
	return in, nil // `sort` is accepted and ignored: categories are alphabetical
}

func colorOf(in apitypes.CategoryInput) *string {
	if in.Color == nil {
		return nil
	}
	c := string(*in.Color)
	return &c
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.categories.List(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]apitypes.Category, len(cats))
	for i, c := range cats {
		out[i] = toCategory(c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	in, err := decodeCategory(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	c, err := s.categories.Create(r.Context(), in.Name, colorOf(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCategory(c))
}

func (s *Server) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	in, err := decodeCategory(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	c, err := s.categories.Update(r.Context(), id, in.Name, colorOf(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toCategory(c))
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var moveTo *int64
	if v := r.URL.Query().Get("move_to"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			s.writeErr(w, r, badf("move_to must be a category id, or 0 for no category"))
			return
		}
		moveTo = &n
	}
	if err := s.categories.Delete(r.Context(), id, moveTo); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
