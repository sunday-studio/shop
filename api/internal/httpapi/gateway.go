package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shop/internal/apitypes"
	"shop/internal/ocr"
	"shop/internal/store"
	"shop/internal/tenancy"
	"shop/internal/version"
)

// Gateway serves the API for any number of households. Every authenticated request resolves its
// tenant from the bearer token, acquires that tenant, and is answered by that tenant's own Server, so
// the request can only ever see the household's own database.
//
// In single mode (tenancy.Single) the gateway is transparent: the one tenant's Server answers the
// public routes too, and the behaviour is the one a Server has on its own. In multi mode the public
// routes carry no household data, and pairing (which would need to know which household a password
// belongs to) is not offered.
type Gateway struct {
	Provider tenancy.Provider
	Resolver tenancy.Resolver
	// Tenant returns the handler of an open tenant (its Server.Inner), building the tenant's service
	// graph on first use.
	Tenant func(ctx context.Context, t *tenancy.Tenant) (http.Handler, error)
	// Info answers /v1/server-info in multi mode. Nil: a minimal hosted answer without login methods.
	Info func(ctx context.Context) ServerInfo
	// OCR and OCRFallback are checked by /healthz in multi mode (process wide, not household data).
	OCR, OCRFallback *ocr.Client
	Log              *slog.Logger
}

// publicPaths are answered by the gateway itself in multi mode and by the default tenant in single mode.
var publicPaths = []string{"GET /healthz", "GET /version", "GET /v1/auth/status", "POST /v1/auth/pair", "GET /v1/server-info"}

func (g *Gateway) Handler() http.Handler {
	if g.Log == nil {
		g.Log = slog.New(slog.DiscardHandler)
	}
	root := http.NewServeMux()
	if g.Provider.Mode() == tenancy.ModeSingle {
		for _, p := range publicPaths {
			root.HandleFunc(p, g.serveDefault)
		}
	} else {
		root.HandleFunc("GET /healthz", g.health)
		root.HandleFunc("GET /version", g.version)
		root.HandleFunc("GET /v1/auth/status", g.authStatus)
		root.HandleFunc("GET /v1/server-info", g.serverInfo)
		root.HandleFunc("POST /v1/auth/pair", func(w http.ResponseWriter, r *http.Request) {
			writeProblemExt(w, http.StatusNotFound, "login method unavailable", "this server does not use a pairing password", "login_method_unavailable", nil)
		})
	}
	root.HandleFunc("/", g.serveTenant)
	return recoverer(g.Log, logRequests(g.Log, root))
}

// serveDefault answers a public route with the only tenant of single mode.
func (g *Gateway) serveDefault(w http.ResponseWriter, r *http.Request) {
	t, release, err := g.Provider.Acquire(r.Context(), tenancy.DefaultID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	defer release()
	h, err := g.Tenant(r.Context(), t)
	if err != nil {
		g.Log.Error("build tenant", "id", t.ID, "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	h.ServeHTTP(w, r)
}

// serveTenant resolves the household from the bearer token and hands the request to its Server.
func (g *Gateway) serveTenant(w http.ResponseWriter, r *http.Request) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="shop"`)
		writeProblem(w, http.StatusUnauthorized, "missing bearer token", "")
		return
	}
	id, err := g.Resolver.Resolve(r.Context(), strings.TrimSpace(raw))
	if err != nil {
		if errors.Is(err, tenancy.ErrUnknownToken) {
			g.invalidToken(w)
			return
		}
		g.Log.Error("resolve token", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	t, release, err := g.Provider.Acquire(r.Context(), id)
	switch {
	case errors.Is(err, tenancy.ErrUnknownTenant):
		g.invalidToken(w) // the token belonged to a household that no longer exists
		return
	case errors.Is(err, tenancy.ErrUnavailable):
		w.Header().Set("Retry-After", "5")
		writeProblemExt(w, http.StatusServiceUnavailable, "household unavailable", "try again soon", "household_unavailable", nil)
		return
	case err != nil:
		g.Log.Error("acquire household", "id", id, "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	defer release()
	h, err := g.Tenant(r.Context(), t)
	if err != nil {
		g.Log.Error("build household", "id", id, "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	h.ServeHTTP(w, r)
}

func (g *Gateway) invalidToken(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="shop", error="invalid_token"`)
	writeProblem(w, http.StatusUnauthorized, "invalid token", "")
}

func (g *Gateway) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]apitypes.Check{}
	check := func(name string, c *ocr.Client) {
		h, err := c.Health(ctx)
		if err != nil {
			msg := err.Error()
			checks[name] = apitypes.Check{Ok: false, Detail: &msg}
			return
		}
		detail := h.Engine
		if !h.Ready {
			detail += " (loading)"
		}
		checks[name] = apitypes.Check{Ok: h.Ready, Detail: &detail}
	}
	if g.OCR != nil {
		check("ocr", g.OCR)
	}
	if g.OCRFallback != nil {
		check("ocr_fallback", g.OCRFallback)
	}
	if o, ok := g.Provider.(interface{ Open() int }); ok {
		detail := strconv.Itoa(o.Open()) + " households open"
		checks["households"] = apitypes.Check{Ok: true, Detail: &detail}
	}
	h := apitypes.Health{Status: apitypes.HealthStatusOk, Checks: checks}
	code := http.StatusOK
	for _, c := range checks {
		if !c.Ok {
			h.Status = apitypes.HealthStatusDegraded
			code = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, code, h)
}

func (g *Gateway) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apitypes.Version{Version: version.Version, Commit: version.Commit, SchemaVersion: store.LatestVersion()})
}

func (g *Gateway) authStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, statusResponse{Server: "shop", Version: version.Version, PairingEnabled: false})
}

func (g *Gateway) serverInfo(w http.ResponseWriter, r *http.Request) {
	var info ServerInfo
	if g.Info != nil {
		info = g.Info(r.Context())
	} else {
		info = ServerInfo{
			Server: "shop", Version: version.Version, APILevel: version.APILevel, Mode: "hosted",
			LoginMethods: []string{}, MinAppBuild: version.MinAppBuild,
			Features: map[string]bool{"households": true, "billing": false, "account_deletion": false, "async_export": false, "mcp": true},
			Limits:   NoLimits(),
		}
	}
	writeServerInfo(w, info)
}
