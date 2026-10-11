package httpapi

import (
	"net/http"
	"strconv"

	"shop/internal/listplan"
)

// Phase C of the shopping list: product groups ("same kind of item") and the shop-by-shop plan.

func (s *Server) getListPlan(w http.ResponseWriter, r *http.Request) {
	var opt listplan.Options
	q := r.URL.Query()
	if v := q.Get("extra_stop_cents"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > listplan.MaxExtraStopCents {
			s.writeErr(w, r, badf("extra_stop_cents must be 0..%d", listplan.MaxExtraStopCents))
			return
		}
		opt.ExtraStopCents = &n
	}
	if v := q.Get("max_stops"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > listplan.MaxMaxStops {
			s.writeErr(w, r, badf("max_stops must be 1..%d", listplan.MaxMaxStops))
			return
		}
		opt.MaxStops = &n
	}
	p, err := s.Plan.Plan(r.Context(), opt)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) getPlanSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Plan.Settings(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) updatePlanSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExtraStopCents *int64 `json:"extra_stop_cents"`
		MaxStops       *int   `json:"max_stops"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	st, err := s.Plan.UpdateSettings(r.Context(), in.ExtraStopCents, in.MaxStops)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) listProductGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Plan.Groups(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if gs == nil {
		gs = []listplan.Group{}
	}
	writeJSON(w, http.StatusOK, gs)
}

func (s *Server) getProductGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	g, err := s.Plan.GetGroup(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) renameProductGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	g, err := s.Plan.Rename(r.Context(), id, in.Name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) confirmProductGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductIDs []int64 `json:"product_ids"`
		Name       string  `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Plan.Confirm(r.Context(), in.ProductIDs, in.Name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) denyProductGroupPair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		A int64 `json:"product_a_id"`
		B int64 `json:"product_b_id"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	op, err := s.Plan.Deny(r.Context(), in.A, in.B)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"op_id": op})
}

func (s *Server) listProductGroupSuggestions(w http.ResponseWriter, r *http.Request) {
	limit, err := s.intParam(r, "limit", listplan.MaxSuggestions, 1, listplan.MaxSuggestions)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sg, err := s.Plan.Suggestions(r.Context(), limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sg)
}

func (s *Server) deleteProductGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Plan.Delete(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) mergeProductGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		Into int64 `json:"into_group_id"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Plan.Merge(r.Context(), id, in.Into)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) splitProductGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		ProductIDs   []int64 `json:"product_ids"`
		KeepTogether bool    `json:"keep_together"`
		Name         string  `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Plan.Split(r.Context(), id, in.ProductIDs, in.KeepTogether, in.Name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) undoProductGroupOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.Plan.Undo(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
