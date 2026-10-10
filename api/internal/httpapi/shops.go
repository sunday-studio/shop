package httpapi

import (
	"net/http"
	"strings"
	"time"

	"pantry/internal/apitypes"
	"pantry/internal/store"
	"pantry/internal/store/db"
)

func toShop(s db.Shop) apitypes.Shop {
	created, _ := store.ParseTS(s.CreatedAt)
	updated, _ := store.ParseTS(s.UpdatedAt)
	return apitypes.Shop{
		Id: s.ID, Name: s.Name, Chain: store.StrPtr(s.Chain), Branch: store.StrPtr(s.Branch),
		Address: store.StrPtr(s.Address), City: store.StrPtr(s.City), Notes: store.StrPtr(s.Notes),
		CreatedAt: created, UpdatedAt: updated,
	}
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
		out[i] = toShop(sh)
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
	writeJSON(w, http.StatusOK, toShop(sh))
}

func (s *Server) createShop(w http.ResponseWriter, r *http.Request) {
	in, err := decodeShop(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := store.Now()
	sh, err := s.q.CreateShop(r.Context(), db.CreateShopParams{
		Name: in.Name, Chain: store.NullStr(in.Chain), Branch: store.NullStr(in.Branch),
		Address: store.NullStr(in.Address), City: store.NullStr(in.City), Notes: store.NullStr(in.Notes),
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toShop(sh))
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
	sh, err := s.q.UpdateShop(r.Context(), db.UpdateShopParams{
		Name: in.Name, Chain: store.NullStr(in.Chain), Branch: store.NullStr(in.Branch),
		Address: store.NullStr(in.Address), City: store.NullStr(in.City), Notes: store.NullStr(in.Notes),
		UpdatedAt: store.Now(), ID: id,
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toShop(sh))
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

func toCategory(c db.Category) apitypes.Category {
	return apitypes.Category{Id: c.ID, Name: c.Name, Sort: int(c.Sort)}
}

func decodeCategory(r *http.Request) (apitypes.CategoryInput, error) {
	var in apitypes.CategoryInput
	if err := decode(r, &in); err != nil {
		return in, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, badf("name is required")
	}
	return in, nil
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.q.ListCategories(r.Context())
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
	sort := int64(0)
	if in.Sort != nil {
		sort = int64(*in.Sort)
	}
	c, err := s.q.CreateCategory(r.Context(), db.CreateCategoryParams{Name: in.Name, Sort: sort})
	if err != nil {
		if isUniqueViolation(err) {
			writeProblem(w, http.StatusConflict, "category exists", in.Name)
			return
		}
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
	cur, err := s.q.GetCategory(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sort := cur.Sort
	if in.Sort != nil {
		sort = int64(*in.Sort)
	}
	c, err := s.q.UpdateCategory(r.Context(), db.UpdateCategoryParams{Name: in.Name, Sort: sort, ID: id})
	if err != nil {
		if isUniqueViolation(err) {
			writeProblem(w, http.StatusConflict, "category exists", in.Name)
			return
		}
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
	n, err := s.q.SoftDeleteCategory(r.Context(), db.SoftDeleteCategoryParams{DeletedAt: store.Str(store.Now()), ID: id})
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

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
