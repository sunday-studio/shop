package hostedapi

import (
	"net/http"
	"time"

	"shop/internal/hosted/control"
)

func (a *API) limitTransactions(p *principal) error {
	return a.limit("txn:"+p.Acc.ID, a.Cfg.Rates.TransactionsPerHr, time.Hour)
}

func (a *API) limitRefresh(p *principal) error {
	return a.limit("refresh:"+p.Acc.ID, a.Cfg.Rates.RefreshPerMin, time.Minute)
}

func (a *API) postTransaction(w http.ResponseWriter, r *http.Request, p *principal) {
	var in struct {
		SignedTransaction string `json:"signed_transaction"`
	}
	if err := decodeJSON(r, &in, false); err != nil || in.SignedTransaction == "" {
		a.fail(w, r, invalid("signed_transaction is required"))
		return
	}
	res, err := a.Bill.BindTransaction(r.Context(), p.Acc.ID, in.SignedTransaction)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	np, err := a.refreshPrincipal(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	acc, err := a.accountShort(r.Context(), np)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var hh any
	if np.HH.ID != "" {
		h, err := a.householdView(r.Context(), np.Acc.ID, false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		hh = h
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res.Result, "account": acc, "household": hh})
}

func (a *API) refreshBilling(w http.ResponseWriter, r *http.Request, p *principal) {
	if _, err := a.Bill.Refresh(r.Context(), p.Acc.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	np, err := a.refreshPrincipal(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	acc, err := a.accountShort(r.Context(), np)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": acc})
}

var _ = control.ErrNotFound
