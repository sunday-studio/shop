package hostedapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/httpapi"
	"shop/internal/version"
)

func (a *API) ipLimit(r *http.Request, scope string, max int, window time.Duration) error {
	return a.limit(scope+":"+a.clientIP(r), max, window)
}

func (a *API) serverInfo(w http.ResponseWriter, r *http.Request) {
	if err := a.ipLimit(r, "info", a.Cfg.Rates.PublicInfoPerMin, time.Minute); err != nil {
		a.fail(w, r, err)
		return
	}
	c := a.Cfg
	minBuild := c.MinAppBuild
	if minBuild == 0 {
		minBuild = version.MinAppBuild
	}
	info := httpapi.ServerInfo{
		Server: "shop", Version: version.Version, APILevel: version.APILevel, Mode: "hosted",
		LoginMethods: []string{"apple"}, PairingEnabled: false, MinAppBuild: minBuild,
		Features: map[string]bool{"households": true, "billing": true, "account_deletion": true, "async_export": true, "mcp": false},
		Limits:   a.infoMap,
		Billing: map[string]any{
			"provider": "apple", "product_ids": c.ProductIDs, "subscription_group": c.SubscriptionGroup, "trial_days": c.TrialDays,
			"storefronts": c.Storefronts, "price_display": "storekit", "manage_subscription_url": c.ManageURL,
		},
		Links: map[string]any{"terms_url": c.TermsURL, "privacy_url": c.PrivacyURL, "support_url": c.SupportURL, "support_email": c.SupportEmail},
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, info)
}

func (a *API) planStates(w http.ResponseWriter, r *http.Request) {
	if err := a.ipLimit(r, "info", a.Cfg.Rates.PublicInfoPerMin, time.Minute); err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"states": billing.StateTable()})
}

func (a *API) appleNonce(w http.ResponseWriter, r *http.Request) {
	if err := a.ipLimit(r, "nonce", a.Cfg.Rates.NoncePerMin, time.Minute); err != nil {
		a.fail(w, r, err)
		return
	}
	nonce, exp, err := a.Auth.IssueNonce(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"nonce": nonce, "expires_at": exp.UTC()})
}

type signInBody struct {
	IdentityToken     string `json:"identity_token"`
	AuthorizationCode string `json:"authorization_code"`
	Nonce             string `json:"nonce"`
	DeviceName        string `json:"device_name"`
	Name              *struct {
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
	} `json:"name"`
}

func (a *API) appleSignIn(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if err := a.limit("signin-min:"+ip, a.Cfg.Rates.SignInPerMin, time.Minute); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.limit("signin-hr:"+ip, a.Cfg.Rates.SignInPerHour, time.Hour); err != nil {
		a.fail(w, r, err)
		return
	}
	var in signInBody
	if err := decodeJSON(r, &in, false); err != nil {
		a.fail(w, r, err)
		return
	}
	req := appleauth.SignInRequest{IdentityToken: in.IdentityToken, AuthorizationCode: in.AuthorizationCode, Nonce: in.Nonce, DeviceName: in.DeviceName}
	if in.Name != nil {
		req.GivenName, req.FamilyName = in.Name.GivenName, in.Name.FamilyName
	}
	res, err := a.Auth.SignIn(r.Context(), req)
	if err != nil {
		if !a.Cfg.Rates.Disabled { // failures count double
			a.rl.add("signin-min:"+ip, time.Minute)
			a.rl.add("signin-hr:"+ip, time.Hour)
		}
		a.fail(w, r, err)
		return
	}
	p, err := a.refreshPrincipal(r.Context(), &principal{Acc: res.Account, Dev: res.Device, IP: ip})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	acc, err := a.accountShort(r.Context(), p)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var hh any
	if p.HH.ID != "" {
		h, err := a.householdView(r.Context(), p.Acc.ID, false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		hh = h
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]any{"created": res.Created, "token": res.Token, "device": deviceView(res.Device, res.Device.ID), "account": acc, "household": hh})
}

func (a *API) appleEvents(w http.ResponseWriter, r *http.Request) {
	if err := a.ipLimit(r, "hook", a.Cfg.Rates.WebhookPerMin, time.Minute); err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Payload string `json:"payload"`
	}
	if err := decodeJSON(r, &in, false); err != nil || in.Payload == "" {
		a.failStatus(w, r, invalid("payload is required"), http.StatusBadRequest)
		return
	}
	if _, err := a.Auth.HandleEvent(r.Context(), in.Payload); err != nil {
		if control.Is(err, appleauth.ErrTokenInvalid) {
			a.failStatus(w, r, appleauth.ErrTokenInvalid, http.StatusBadRequest)
			return
		}
		if control.Is(err, appleauth.ErrUnavailable) {
			a.fail(w, r, err)
			return
		}
		a.fail(w, r, errors.New("event: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (a *API) appleNotifications(w http.ResponseWriter, r *http.Request) {
	if err := a.ipLimit(r, "hook", a.Cfg.Rates.WebhookPerMin, time.Minute); err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		SignedPayload string `json:"signedPayload"`
	}
	if err := decodeJSON(r, &in, false); err != nil || in.SignedPayload == "" {
		a.failStatus(w, r, invalid("signedPayload is required"), http.StatusBadRequest)
		return
	}
	if _, err := a.Bill.ProcessNotification(r.Context(), in.SignedPayload); err != nil {
		if control.Is(err, billing.ErrNotificationInvalid) {
			a.failStatus(w, r, billing.ErrNotificationInvalid, http.StatusBadRequest)
			return
		}
		a.fail(w, r, err) // 500: Apple retries
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// invitePreview is the public invite check (design ask 1): it reveals only whether the code is
// usable and the owner's display name, and is rate limited like accepting.
func (a *API) invitePreview(w http.ResponseWriter, r *http.Request) {
	if err := a.ipLimit(r, "preview", a.Cfg.Rates.AcceptPerHour, time.Hour); err != nil {
		a.fail(w, r, err)
		return
	}
	out := map[string]any{"valid": false, "owner_display_name": nil}
	inv, err := a.Store.InviteByHash(r.Context(), control.HashCode(r.PathValue("code")))
	if err == nil && inv.Open(a.Store.Now()) {
		if h, herr := a.Store.HouseholdByID(r.Context(), inv.HouseholdID); herr == nil && h.Status == control.HouseholdActive {
			out["valid"] = true
			if name, nerr := a.Store.HouseholdOwnerName(r.Context(), h.ID); nerr == nil && name != "" {
				out["owner_display_name"] = name
			}
		}
	} else if err != nil && !control.Is(err, control.ErrInviteInvalid) {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

var _ = io.EOF
