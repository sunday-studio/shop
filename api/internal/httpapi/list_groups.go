package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"shop/internal/apitypes"
	"shop/internal/shopping"
	"shop/internal/store"
)

// Phase B: groups (checklists and templates) inside the shopping list.

func (s *Server) setListRev(w http.ResponseWriter, r *http.Request) {
	if rev, err := s.List.Rev(r.Context()); err == nil {
		w.Header().Set("X-List-Rev", strconv.FormatInt(rev, 10))
	}
}

func toGroup(g shopping.Group) map[string]any {
	created, _ := store.ParseTS(g.CreatedAt)
	updated, _ := store.ParseTS(g.UpdatedAt)
	out := map[string]any{
		"id": g.ID, "name": g.Name, "kind": g.Kind, "source": g.Source, "sort": g.Sort,
		"created_at": created, "updated_at": updated, "rev": g.Rev,
		"item_count": g.ItemCount, "checked_count": g.CheckedCount,
	}
	if g.Note.Valid {
		out["note"] = g.Note.String
	}
	if g.ExternalRef.Valid {
		out["external_ref"] = g.ExternalRef.String
	}
	if t := store.TimePtr(g.ArchivedAt); t != nil {
		out["archived_at"] = *t
	}
	if g.Items != nil {
		out["items"] = toItems(g.Items)
	}
	return out
}

func toGroups(in []shopping.Group) []map[string]any {
	out := make([]map[string]any, len(in))
	for i, g := range in {
		out[i] = toGroup(g)
	}
	return out
}

func toGroupResult(res shopping.GroupResult) map[string]any {
	out := toGroup(res.Group)
	out["item_ids"] = res.ItemIDs
	return out
}

func writeGroupResult(w http.ResponseWriter, res shopping.GroupResult) {
	code := http.StatusOK
	if res.Created {
		code = http.StatusCreated
	}
	writeJSON(w, code, toGroupResult(res))
}

func listInputs(in []apitypes.ListItemInput) []shopping.Input {
	out := make([]shopping.Input, len(in))
	for i, l := range in {
		out[i] = listInput(l)
	}
	return out
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	s.setListRev(w, r)
	q := r.URL.Query()
	gs, err := s.List.ListGroups(r.Context(), q.Get("kind"), q.Get("include_archived") == "true", q.Get("include_items") == "true")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toGroups(gs))
}

func (s *Server) createListGroup(w http.ResponseWriter, r *http.Request) {
	var in apitypes.ListGroupInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	gi := shopping.GroupInput{Name: in.Name, Note: in.Note, Sort: in.Sort, Items: listInputs(deref(in.Items))}
	if in.ClientId != nil {
		gi.ClientID = *in.ClientId
	}
	if gi.ClientID == "" {
		gi.ClientID = r.Header.Get("Idempotency-Key")
	}
	if in.Kind != nil {
		gi.Kind = string(*in.Kind)
	}
	if in.Source != nil {
		gi.Source = string(*in.Source)
	}
	if in.ExternalRef != nil {
		gi.ExternalRef = *in.ExternalRef
	}
	res, err := s.List.CreateGroup(r.Context(), gi)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeGroupResult(w, res)
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Server) getListGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	g, err := s.List.GetGroup(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toGroup(g))
}

func (s *Server) updateListGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.ListGroupPatch
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	g, err := s.List.UpdateGroup(r.Context(), id, shopping.GroupPatch{Name: in.Name, Note: in.Note, Sort: in.Sort, Archived: in.Archived})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toGroup(g))
}

func (s *Server) deleteListGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	keep := true
	switch v := r.URL.Query().Get("items"); v {
	case "", "keep":
	case "delete":
		keep = false
	default:
		s.writeErr(w, r, badf("items must be keep or delete"))
		return
	}
	res, err := s.List.DeleteGroup(r.Context(), id, keep)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group_id": id, "kept": toItems(res.Kept), "deleted_item_ids": res.DeletedItemIDs, "restore_until": res.RestoreUntil,
	})
}

func (s *Server) restoreListGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	g, err := s.List.RestoreGroup(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toGroup(g))
}

func (s *Server) addListGroupItems(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in apitypes.ListGroupItemsInput
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	src := ""
	if in.Source != nil {
		src = string(*in.Source)
	}
	items, err := s.List.AddItems(r.Context(), id, listInputs(in.Items), src)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toItems(items))
}

func (s *Server) copyListGroup(save bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		var in apitypes.ListGroupCopyInput
		if r.ContentLength != 0 {
			if err := decode(r, &in); err != nil {
				s.writeErr(w, r, err)
				return
			}
		}
		name, cid := "", r.Header.Get("Idempotency-Key")
		if in.Name != nil {
			name = *in.Name
		}
		if in.ClientId != nil {
			cid = *in.ClientId
		}
		var res shopping.GroupResult
		if save {
			res, err = s.List.SaveAsTemplate(r.Context(), id, name, cid)
		} else {
			res, err = s.List.Instantiate(r.Context(), id, name, cid, "")
		}
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		writeGroupResult(w, res)
	}
}

func (s *Server) moveListItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		GroupID json.RawMessage `json:"group_id"`
	}
	if err := decode(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if in.GroupID == nil {
		s.writeErr(w, r, badf("group_id is required (a group id, or null for the main list)"))
		return
	}
	var gid *int64
	if err := json.Unmarshal(in.GroupID, &gid); err != nil {
		s.writeErr(w, r, badf("group_id must be a number or null"))
		return
	}
	it, err := s.List.MoveItem(r.Context(), id, gid)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toListItem(it))
}
