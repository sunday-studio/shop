package hostedapi

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/limits"
	"shop/internal/httpapi"
	"shop/internal/tenancy"
	"shop/internal/tenantapp"
)

// Deps are the services the API is built from.
type Deps struct {
	Cfg      Config
	Clock    hclock.Clock
	Log      *slog.Logger
	Ctl      *control.Service
	Calc     *billing.Calculator
	Bill     *billing.Service
	Auth     *appleauth.Service
	Grants   *grants.Service // admin routes
	Meter    *limits.Meter
	Gate     *limits.Gate // optional; created when nil
	Provider *tenancy.Multi
	Runtime  *tenantapp.Runtime
	// Sealer encrypts the stored idempotency answers (the key lives in the server environment, never in
	// the database). Optional: nil makes a random per-process key (a restart then forgets replays).
	Sealer appleauth.Sealer
	// Dev serves /admin/v1/dev/{what} on the admin listener (the fake Apple of local stacks); nil: no such routes.
	Dev func(w http.ResponseWriter, r *http.Request, what string)
}

// API is the hosted HTTP layer.
type API struct {
	Deps
	Store *control.Store

	rl      *rateLimiter
	upSlots chan struct{} // process-wide cap on receipt uploads in flight
	gw      *httpapi.Gateway
	gwH     http.Handler
	exports *exportRunner
	infoMap map[string]any
}

// New builds the API. Nothing runs in the background until StartExports is called.
func New(d Deps) (*API, error) {
	if d.Clock == nil {
		d.Clock = d.Ctl.S.Clock()
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Cfg.ExportTTL == 0 {
		d.Cfg.ExportTTL = 7 * 24 * time.Hour
	}
	if d.Cfg.IdempotencyTTL == 0 {
		d.Cfg.IdempotencyTTL = 24 * time.Hour
	}
	if d.Cfg.Rates == (Rates{}) {
		d.Cfg.Rates = DefaultRates()
	}
	if d.Cfg.ExportDir == "" {
		return nil, errors.New("hostedapi: Config.ExportDir is required")
	}
	if d.Gate == nil {
		d.Gate = limits.NewGate(0)
	}
	if d.Sealer == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		sl, err := appleauth.NewAESGCMSealer(key)
		if err != nil {
			return nil, err
		}
		d.Sealer = sl
	}
	a := &API{Deps: d, Store: d.Ctl.S, rl: newRateLimiter(d.Clock)}
	if n := d.Cfg.Rates.UploadsInFlight; n > 0 {
		a.upSlots = make(chan struct{}, n)
	}
	a.gw = d.Runtime.Gateway(d.Provider, ctxResolver{fallback: tenancy.DirectoryResolver{Dir: a.Directory()}})
	a.gwH = a.gw.Handler()
	a.exports = newExportRunner(a)
	a.infoMap = a.buildInfoLimits()
	return a, nil
}

// Gateway exposes the tenancy gateway (tests).
func (a *API) Gateway() http.Handler { return a.gwH }

// ctxResolver takes the household from the authenticated request (set by this package), and falls
// back to the control-database directory for anything else.
type ctxResolver struct{ fallback tenancy.Resolver }

func (r ctxResolver) Resolve(ctx context.Context, bearer string) (string, error) {
	if p := principalFrom(ctx); p != nil && p.HH.ID != "" {
		return p.HH.ID, nil
	}
	return r.fallback.Resolve(ctx, bearer)
}

// Handler is the public API.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	pass := func(h http.Handler) http.Handler { return h }
	_ = pass

	// Public routes that the gateway answers (health, version, status, pairing refusal).
	mux.HandleFunc("GET /healthz", a.publicHealth)
	for _, p := range []string{"GET /version", "GET /v1/auth/status", "POST /v1/auth/pair"} {
		mux.Handle(p, a.gwH)
	}
	mux.Handle("GET /v1/server-info", a.logged(http.HandlerFunc(a.serverInfo)))
	mux.Handle("GET /v1/billing/plan-states", a.logged(http.HandlerFunc(a.planStates)))
	mux.Handle("POST /v1/auth/apple/nonce", a.logged(http.HandlerFunc(a.appleNonce)))
	mux.Handle("POST /v1/auth/apple", a.logged(http.HandlerFunc(a.appleSignIn)))
	mux.Handle("POST /v1/auth/apple/events", a.logged(http.HandlerFunc(a.appleEvents)))
	mux.Handle("POST /v1/billing/apple/notifications", a.logged(http.HandlerFunc(a.appleNotifications)))
	mux.Handle("GET /v1/household/invites/{code}", a.logged(http.HandlerFunc(a.invitePreview)))

	r := func(pattern string, o routeOpts, h func(http.ResponseWriter, *http.Request, *principal)) {
		mux.Handle(pattern, a.logged(a.authed(o, h)))
	}
	r("GET /v1/auth/me", routeOpts{}, a.me)
	r("GET /v1/auth/devices", routeOpts{}, a.listDevices)
	r("DELETE /v1/auth/devices/{id}", routeOpts{}, a.revokeDevice)

	r("GET /v1/account", routeOpts{}, a.getAccount)
	r("PATCH /v1/account", routeOpts{}, a.patchAccount)
	r("DELETE /v1/account", routeOpts{}, a.deleteAccount)
	r("DELETE /v1/account/deletion", routeOpts{}, a.cancelDeletion)
	r("POST /v1/account/export", routeOpts{}, a.createExport)
	r("GET /v1/account/export", routeOpts{}, a.getExport)
	r("GET /v1/account/export/{id}/download", routeOpts{}, a.downloadExport)

	r("GET /v1/household", routeOpts{}, a.getHousehold)
	r("POST /v1/household/invites", routeOpts{Owner: true, Action: billing.ActInvite}, a.createInvite)
	r("DELETE /v1/household/invites/{id}", routeOpts{Owner: true, Action: billing.ActManageHousehold}, a.revokeInvite)
	r("POST /v1/household/invites/{code}/accept", routeOpts{Limit: a.limitAccept}, a.acceptInvite)
	r("DELETE /v1/household/members/{account_id}", routeOpts{Owner: true, Action: billing.ActManageHousehold}, a.removeMember)
	r("POST /v1/household/leave", routeOpts{}, a.leaveHousehold)
	r("POST /v1/household/transfer", routeOpts{Owner: true, Action: billing.ActManageHousehold}, a.transferHousehold)

	r("POST /v1/billing/apple/transactions", routeOpts{Limit: a.limitTransactions}, a.postTransaction)
	r("POST /v1/billing/apple/refresh", routeOpts{Limit: a.limitRefresh}, a.refreshBilling)

	// Everything else is the household's own API behind the tenant chain.
	mux.Handle("/", a.tenantChain())
	return mux
}

// logged recovers from panics and logs the request (the gateway does the same for tenant routes).
func (a *API) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRec{ResponseWriter: w, status: 200}
		defer func() {
			if v := recover(); v != nil {
				a.Log.Error("panic", "path", r.URL.Path, "panic", v)
				if !rec.wrote {
					writeProblem(rec, problem{Status: 500, Code: "internal_error", Detail: "Something went wrong on our side."}, nil)
				}
			}
			a.Log.Info("http", "method", r.Method, "path", logPath(r.URL.Path), "status", rec.status, "ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(rec, r)
	})
}

// logPath is the request path as it may appear in logs: an invite code in the path is a bearer
// secret (it lets anyone who has it join the household), so it never reaches the log.
func logPath(p string) string {
	const pre = "/v1/household/invites/"
	rest, ok := strings.CutPrefix(p, pre)
	if !ok {
		return p
	}
	code, tail, _ := strings.Cut(rest, "/")
	if code == "" {
		return p
	}
	if tail != "" {
		tail = "/" + tail
	}
	return pre + "{code}" + tail
}

type statusRec struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRec) WriteHeader(c int) {
	s.status, s.wrote = c, true
	s.ResponseWriter.WriteHeader(c)
}
func (s *statusRec) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}
func (s *statusRec) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (s *statusRec) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// ---- principal ----

type ctxKey int

const principalKey ctxKey = 1

// principal is who makes the request: the account, the device, the household (if any) and the plan.
type principal struct {
	Acc  control.Account
	Dev  control.Device
	HH   control.Household // zero ID: none
	Role string            // owner, member or none
	Plan billing.Plan
	IP   string
}

func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey).(*principal)
	return p
}

// authenticate resolves the bearer token, the household and the plan.
func (a *API) authenticate(r *http.Request) (*principal, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	raw = strings.TrimSpace(raw)
	if !ok || raw == "" {
		return nil, errTokenInvalid
	}
	ctx := r.Context()
	pr, err := a.Ctl.Authenticate(ctx, raw)
	switch {
	case control.Is(err, control.ErrAccountLocked):
		return nil, control.ErrAccountLocked
	case control.Is(err, control.ErrNotFound):
		return nil, errTokenInvalid
	case err != nil:
		return nil, err
	}
	p := &principal{Acc: pr.Account, Dev: pr.Device, Role: "none", IP: a.clientIP(r)}
	h, m, err := a.Store.HouseholdOfAccount(ctx, p.Acc.ID)
	switch {
	case err == nil:
		p.HH, p.Role = h, m.Role
	case control.Is(err, control.ErrNoHousehold):
	default:
		return nil, err
	}
	if p.Plan, err = a.Calc.ForAccount(ctx, p.Acc.ID); err != nil {
		return nil, err
	}
	return p, nil
}

// gate refuses an action the plan does not allow (403 plan_required or read_only).
func (a *API) gate(p *principal, action string) error {
	if action == "" || p.Plan.Can(action) {
		return nil
	}
	var after time.Time
	if p.Plan.DeleteAfter != nil {
		after = *p.Plan.DeleteAfter
	}
	return planGateErr(p.Plan.State, after)
}

type routeOpts struct {
	Owner  bool   // only the household owner (403 not_household_owner)
	Action string // plan gate
	// Limit is a route specific rate limit, run after the plan gate.
	Limit func(p *principal) error
}

// authed wraps a handler with token, owner and plan checks, the per-token rate limit and idempotency.
func (a *API) authed(o routeOpts, h func(http.ResponseWriter, *http.Request, *principal)) http.Handler {
	inner := func(w http.ResponseWriter, r *http.Request, p *principal) {
		if err := a.limit("tok:"+itoa(p.Dev.ID), a.Cfg.Rates.TokenPerMin, time.Minute); err != nil {
			a.fail(w, r, err)
			return
		}
		if o.Limit != nil {
			if err := o.Limit(p); err != nil {
				a.fail(w, r, err)
				return
			}
		}
		a.idempotent(p.Acc.ID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h(w, r, p) })).ServeHTTP(w, r)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.authenticate(r)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
		if o.Owner && p.Role != control.RoleOwner {
			a.fail(w, r, control.ErrNotOwner)
			return
		}
		if err := a.gate(p, o.Action); err != nil {
			a.fail(w, r, err)
			return
		}
		inner(w, r, p)
	})
}
