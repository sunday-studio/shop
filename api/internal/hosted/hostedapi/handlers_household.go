package hostedapi

import (
	"net/http"
	"strings"
	"time"

	"shop/internal/hosted/control"
)

func (a *API) getHousehold(w http.ResponseWriter, r *http.Request, p *principal) {
	out, err := a.householdView(r.Context(), p.Acc.ID, true)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request, p *principal) {
	inv, code, err := a.Ctl.CreateInvite(r.Context(), p.Acc.ID)
	if err != nil {
		if control.Is(err, control.ErrHouseholdFull) {
			n, _ := a.Store.CountMembers(r.Context(), p.HH.ID)
			open, _ := a.Store.OpenInvites(r.Context(), p.HH.ID, a.Store.Now())
			err = &httpErr{Status: http.StatusConflict, Code: "household_full", Detail: "A household can have up to 4 people.",
				Extra: func(pr *problem) {
					v, c := int64(a.Ctl.Cfg.MemberCap), int64(n+len(open))
					pr.Limit, pr.LimitValue, pr.Current = "household_members", &v, &c
				}}
		}
		a.fail(w, r, err)
		return
	}
	base := strings.TrimRight(a.Cfg.InviteBaseURL, "/")
	writeJSON(w, http.StatusCreated, map[string]any{"id": inv.ID, "code": code, "url": base + "/" + code,
		"created_at": inv.CreatedAt.UTC(), "expires_at": inv.ExpiresAt.UTC()})
}

func (a *API) revokeInvite(w http.ResponseWriter, r *http.Request, p *principal) {
	if err := a.Ctl.RevokeInvite(r.Context(), p.Acc.ID, r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) limitAccept(p *principal) error {
	if err := a.limit("accept-acc:"+p.Acc.ID, a.Cfg.Rates.AcceptPerHour, time.Hour); err != nil {
		return err
	}
	return a.limit("accept-ip:"+p.IP, a.Cfg.Rates.AcceptPerHour, time.Hour)
}

func (a *API) acceptInvite(w http.ResponseWriter, r *http.Request, p *principal) {
	ctx := r.Context()
	code := r.PathValue("code")
	// A repeat after success returns the household again (lost responses).
	if inv, err := a.Store.InviteByHash(ctx, control.HashCode(code)); err == nil && inv.UsedBy == p.Acc.ID && p.HH.ID == inv.HouseholdID {
		a.acceptDone(w, r, p)
		return
	}
	if _, err := a.Ctl.AcceptInvite(ctx, p.Acc.ID, code); err != nil {
		if control.Is(err, control.ErrHouseholdFull) {
			v, c := int64(a.Ctl.Cfg.MemberCap), int64(a.Ctl.Cfg.MemberCap)
			err = &httpErr{Status: http.StatusConflict, Code: "household_full", Detail: "A household can have up to 4 people.",
				Extra: func(pr *problem) { pr.Limit, pr.LimitValue, pr.Current = "household_members", &v, &c }}
		}
		a.fail(w, r, err)
		return
	}
	np, err := a.refreshPrincipal(ctx, p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.acceptDone(w, r, np)
}

func (a *API) acceptDone(w http.ResponseWriter, r *http.Request, p *principal) {
	np, err := a.refreshPrincipal(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	hh, err := a.householdView(r.Context(), np.Acc.ID, false)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	acc, err := a.accountShort(r.Context(), np)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"household": hh, "account": acc})
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request, p *principal) {
	if err := a.Ctl.RemoveMember(r.Context(), p.Acc.ID, r.PathValue("account_id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) leaveHousehold(w http.ResponseWriter, r *http.Request, p *principal) {
	if err := a.Ctl.Leave(r.Context(), p.Acc.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) transferHousehold(w http.ResponseWriter, r *http.Request, p *principal) {
	var in struct {
		AccountID         string `json:"account_id"`
		ConfirmPlanChange bool   `json:"confirm_plan_change"`
	}
	if err := decodeJSON(r, &in, false); err != nil {
		a.fail(w, r, err)
		return
	}
	if in.AccountID == "" {
		a.fail(w, r, invalid("account_id is required"))
		return
	}
	if _, err := a.Ctl.Transfer(r.Context(), p.Acc.ID, in.AccountID, in.ConfirmPlanChange); err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := a.householdView(r.Context(), p.Acc.ID, false)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
