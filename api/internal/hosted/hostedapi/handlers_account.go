package hostedapi

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"shop/internal/hosted/control"
)

func (a *API) me(w http.ResponseWriter, r *http.Request, p *principal) {
	out := map[string]any{"name": p.Dev.Name, "kind": "device", "account_id": p.Acc.ID, "household_id": nil}
	if p.HH.ID != "" {
		out["household_id"] = p.HH.ID
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listDevices(w http.ResponseWriter, r *http.Request, p *principal) {
	ds, err := a.Store.ListDevices(r.Context(), p.Acc.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]deviceOut, 0, len(ds))
	for _, d := range ds {
		out = append(out, deviceView(d, p.Dev.ID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) revokeDevice(w http.ResponseWriter, r *http.Request, p *principal) {
	var id int64
	for _, c := range r.PathValue("id") {
		if c < '0' || c > '9' || id > 1<<50 {
			a.fail(w, r, control.ErrNotFound)
			return
		}
		id = id*10 + int64(c-'0')
	}
	ok, err := a.Store.RevokeDevice(r.Context(), id, p.Acc.ID, a.Store.Now())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !ok {
		a.fail(w, r, control.ErrNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getAccount(w http.ResponseWriter, r *http.Request, p *principal) {
	out, err := a.accountFullView(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) patchAccount(w http.ResponseWriter, r *http.Request, p *principal) {
	var in struct {
		DisplayName *string `json:"display_name"`
	}
	if err := decodeJSON(r, &in, false); err != nil {
		a.fail(w, r, err)
		return
	}
	if in.DisplayName == nil {
		a.fail(w, r, invalid("display_name is required"))
		return
	}
	name := strings.TrimSpace(*in.DisplayName)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		a.fail(w, r, invalid("display_name must be 1 to 60 characters"))
		return
	}
	if err := a.Store.SetAccountDisplayName(r.Context(), p.Acc.ID, name); err != nil {
		a.fail(w, r, err)
		return
	}
	np, err := a.refreshPrincipal(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := a.accountFullView(r.Context(), np)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// subscriptionStillActive: the account (or its household's owner) still has an Apple subscription
// that Apple will keep billing or that has not run out.
func (a *API) subscriptionStillActive(r *http.Request, p *principal) bool {
	owner := p.Acc.ID
	if p.HH.ID != "" {
		owner = p.HH.OwnerAccountID
	}
	ents, err := a.Store.EntitlementsForAccount(r.Context(), owner)
	if err != nil {
		return false
	}
	now := a.Store.Now()
	for _, e := range ents {
		if e.Source != control.SourceApple {
			continue
		}
		switch e.State {
		case control.EntActive, control.EntGrace, control.EntBillingRetry:
			if e.State != control.EntActive || e.ExpiresAt.IsZero() || e.ExpiresAt.After(now) {
				return true
			}
		}
	}
	return false
}

func (a *API) deleteAccount(w http.ResponseWriter, r *http.Request, p *principal) {
	var in struct {
		Confirm         string `json:"confirm"`
		DeleteHousehold bool   `json:"delete_household"`
	}
	if err := decodeJSON(r, &in, false); err != nil {
		a.fail(w, r, err)
		return
	}
	acc, err := a.Store.AccountByID(r.Context(), p.Acc.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	repeat := !acc.DeletionRequestedAt.IsZero()
	st, err := a.Ctl.RequestAccountDeletion(r.Context(), p.Acc.ID, in.Confirm, in.DeleteHousehold, p.Dev.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !repeat {
		// Revoking at Apple must not fail the request; a failure is retried by the lifecycle job.
		if _, rerr := a.Auth.RevokePending(r.Context(), p.Acc.ID); rerr != nil {
			a.Log.Warn("revoke apple token", "err", rerr)
		}
	}
	manage := a.Cfg.ManageURL
	status := http.StatusAccepted
	if repeat {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"status": "scheduled", "requested_at": st.RequestedAt.UTC(), "delete_after": st.DeleteAfter.UTC(),
		"subscription_still_active": a.subscriptionStillActive(r, p), "manage_subscription_url": manage,
		"can_cancel": a.Store.Now().Before(st.DeleteAfter),
	})
}

func (a *API) cancelDeletion(w http.ResponseWriter, r *http.Request, p *principal) {
	if err := a.Ctl.CancelAccountDeletion(r.Context(), p.Acc.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	np, err := a.refreshPrincipal(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := a.accountFullView(r.Context(), np)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

var _ = time.Second
