package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"pantry/internal/apitypes"
	"pantry/internal/shopping"
	"pantry/internal/stats"
	"pantry/internal/store"
)

// Stats handlers serialise the stats structs directly; their JSON tags
// mirror openapi.yaml (tests decode responses into the generated types).

func (s *Server) optInt64(r *http.Request, name string) (*int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, badf("invalid %s", name)
	}
	return &n, nil
}

func (s *Server) intParam(r *http.Request, name string, def, lo, hi int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, badf("%s must be %d..%d", name, lo, hi)
	}
	return n, nil
}

func (s *Server) getSpend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	today := time.Now().In(s.Location)
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, s.Location)
	from, to := todayStart.AddDate(0, 0, -29), todayStart.AddDate(0, 0, 1)
	if f, err := s.localDate(q.Get("from"), 0); err != nil {
		s.writeErr(w, r, err)
		return
	} else if f != nil {
		from = *f
	}
	if t, err := s.localDate(q.Get("to"), 1); err != nil {
		s.writeErr(w, r, err)
		return
	} else if t != nil {
		to = *t
	}
	if !from.Before(to) {
		s.writeErr(w, r, badf("from must be before to"))
		return
	}
	group := stats.ByMonth
	if v := q.Get("group_by"); v != "" {
		group = stats.GroupBy(v)
		if !group.Valid() {
			s.writeErr(w, r, badf("group_by must be day, week, month, shop or category"))
			return
		}
	}
	shop, err := s.optInt64(r, "shop_id")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Stats.Spend(r.Context(), from, to, group, shop)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) productExists(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return 0, false
	}
	if _, err := s.Products.Get(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return 0, false
	}
	return id, true
}

func (s *Server) getPriceHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.productExists(w, r)
	if !ok {
		return
	}
	shop, err := s.optInt64(r, "shop_id")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	pts, err := s.Stats.PriceHistory(r.Context(), id, shop)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pts)
}

func (s *Server) comparePrices(w http.ResponseWriter, r *http.Request) {
	id, ok := s.productExists(w, r)
	if !ok {
		return
	}
	res, err := s.Stats.Compare(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getPriceChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := time.Now().AddDate(0, 0, -90)
	if t, err := s.localDate(q.Get("since"), 0); err != nil {
		s.writeErr(w, r, err)
		return
	} else if t != nil {
		since = *t
	}
	minPct := 5.0
	if v := q.Get("min_pct"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			s.writeErr(w, r, badf("invalid min_pct"))
			return
		}
		minPct = f
	}
	res, err := s.Stats.PriceChanges(r.Context(), since, minPct)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getFrequentItems(w http.ResponseWriter, r *http.Request) {
	lookback, err := s.intParam(r, "lookback_days", 180, 1, 730)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	minCount, err := s.intParam(r, "min_count", 3, 2, 1000)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Stats.FrequentItems(r.Context(), lookback, minCount, time.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------- list

func toListItem(it shopping.Item) map[string]any {
	created, _ := store.ParseTS(it.CreatedAt)
	out := map[string]any{"id": it.ID, "text": it.Text, "created_at": created}
	if it.ProductID.Valid {
		out["product_id"] = it.ProductID.Int64
		out["product_name"] = it.ProductName
	}
	if it.Qty.Valid {
		out["qty"] = it.Qty.Float64
	}
	if it.Note.Valid {
		out["note"] = it.Note.String
	}
	if t := store.TimePtr(it.CheckedAt); t != nil {
		out["checked_at"] = *t
	}
	if it.Cheapest != nil {
		out["cheapest"] = it.Cheapest
	}
	return out
}

func listInput(in apitypes.ListItemInput) shopping.Input {
	out := shopping.Input{ProductID: in.ProductId, Qty: in.Qty, Note: in.Note}
	if in.Text != nil {
		out.Text = *in.Text
	}
	return out
}

func (s *Server) getShoppingList(w http.ResponseWriter, r *http.Request) {
	items, err := s.List.List(r.Context(), r.URL.Query().Get("include_checked") == "true")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = toListItem(it)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) addListItem(w http.ResponseWriter, r *http.Request) {
	var in apitypes.ListItemInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	it, created, err := s.List.Add(r.Context(), listInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, toListItem(it))
}

func (s *Server) updateListItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.ListItemInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	it, err := s.List.Update(r.Context(), id, listInput(in))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toListItem(it))
}

func (s *Server) setListItemChecked(checked bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		it, err := s.List.SetChecked(r.Context(), id, checked)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toListItem(it))
	}
}

func (s *Server) deleteListItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.List.Delete(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearCheckedItems(w http.ResponseWriter, r *http.Request) {
	n, err := s.List.ClearChecked(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"removed": n})
}
