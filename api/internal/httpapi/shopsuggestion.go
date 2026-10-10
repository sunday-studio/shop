package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"shop/internal/apitypes"
	"shop/internal/shopsuggest"
)

func toShopSuggestion(s *shopsuggest.Suggestion) *apitypes.ShopSuggestion {
	if s == nil {
		return nil
	}
	out := &apitypes.ShopSuggestion{
		Kind: apitypes.ShopSuggestionKind(s.Kind), ShopId: s.ShopID, Name: s.Name,
		Confidence: float32(s.Conf), Evidence: s.Evidence, WaitingReceiptIds: s.Waiting,
	}
	if out.Evidence == nil {
		out.Evidence = []string{}
	}
	if out.WaitingReceiptIds == nil {
		out.WaitingReceiptIds = []int64{}
	}
	if s.Chain != "" {
		out.Chain = &s.Chain
	}
	if s.Branch != "" {
		out.Branch = &s.Branch
	}
	if s.Domain != "" {
		out.Domain = &s.Domain
	}
	return out
}

func (s *Server) getShopSuggestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sug, err := s.suggest.Suggest(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipt_id": id, "suggestion": toShopSuggestion(sug)})
}

func (s *Server) acceptShopSuggestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var in struct {
		ShopID         *int64 `json:"shop_id"`
		Name           string `json:"name"`
		Branch         string `json:"branch"`
		Domain         string `json:"domain"`
		ApplyToWaiting *bool  `json:"apply_to_waiting"`
	}
	if body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16)); err != nil {
		s.writeErr(w, r, badf("could not read body"))
		return
	} else if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			s.writeErr(w, r, badf("invalid JSON body"))
			return
		}
	}
	apply := in.ApplyToWaiting == nil || *in.ApplyToWaiting
	shop, moved, err := s.suggest.Accept(r.Context(), id, in.ShopID, in.Name, in.Branch, in.Domain, apply)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if moved == nil {
		moved = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shop": s.toShop(shop), "linked_receipt_ids": moved})
}

func (s *Server) rejectShopSuggestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.suggest.Reject(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
