package hostedapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/httpapi"
	"shop/internal/tenancy"
)

// Directory implements tenancy.Directory on the control database: device token hash -> account ->
// household. Revoked devices, locked accounts and accounts without a household are unknown tokens.
func (a *API) Directory() tenancy.Directory { return directory{a.Store} }

type directory struct{ s *control.Store }

func (d directory) HouseholdForToken(ctx context.Context, tokenHash string) (string, error) {
	dev, err := d.s.DeviceByTokenHash(ctx, tokenHash)
	if err != nil || !dev.RevokedAt.IsZero() {
		return "", tenancy.ErrUnknownToken
	}
	acc, err := d.s.AccountByID(ctx, dev.AccountID)
	if err != nil || acc.Status != control.AccountActive {
		return "", tenancy.ErrUnknownToken
	}
	h, _, err := d.s.HouseholdOfAccount(ctx, acc.ID)
	if err != nil || h.Status == control.HouseholdDeleted {
		return "", tenancy.ErrUnknownToken
	}
	return h.ID, nil
}

// actionFor classifies a request on the household API: read, write or upload.
func actionFor(r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return billing.ActRead
	}
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/v1/receipts":
			return billing.ActUpload
		case "/v1/glossary/preview": // pure computation
			return billing.ActRead
		}
	}
	return billing.ActWrite
}

// tenantChain is the middleware chain in front of the households' own API.
func (a *API) tenantChain() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		p, err := a.authenticate(r)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		r = r.WithContext(ctx)
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
			a.fail(w, r, &httpErr{Status: http.StatusNotFound, Code: "feature_unavailable", Detail: "MCP is not available on the hosted service.",
				Extra: func(pr *problem) { pr.Limit = "mcp" }})
			return
		}
		action := actionFor(r)
		if p.HH.ID == "" {
			a.fail(w, r, planGateErr(p.Plan.State, time.Time{}))
			return
		}
		if err := a.gate(p, action); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := a.limit("tok:"+itoa(p.Dev.ID), a.Cfg.Rates.TokenPerMin, time.Minute); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := a.provision(r.Context(), p.HH.ID); err != nil {
			a.fail(w, r, err)
			return
		}
		lim, err := a.Meter.LimitsFor(r.Context(), p.HH.ID)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		inflight := lim.ConcurrentRequests
		if a.Cfg.Rates.InFlightPerHousehold > 0 && inflight == 0 {
			inflight = int64(a.Cfg.Rates.InFlightPerHousehold)
		}
		release, err := a.Gate.Acquire(p.HH.ID, inflight)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		defer release()
		if action == billing.ActUpload {
			if err := a.limit("upl:"+p.HH.ID, a.Cfg.Rates.UploadsPerMin, time.Minute); err != nil {
				a.fail(w, r, err)
				return
			}
			if r.ContentLength > lim.MaxUploadBytes+(1<<20) && lim.MaxUploadBytes > 0 {
				a.fail(w, r, &control.LimitError{Limit: "max_upload_bytes", Value: lim.MaxUploadBytes, Current: r.ContentLength, FileTooLarge: true})
				return
			}
			ctx = httpapi.WithUploadGuard(ctx, a.uploadGuard(p))
		}
		ctx = httpapi.WithExternalAuth(ctx, httpapi.ExternalAuth{DeviceID: p.Dev.ID, Name: p.Dev.Name, Kind: "device"})
		_ = start
		a.idempotent(p.Acc.ID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.gwH.ServeHTTP(w, r)
		})).ServeHTTP(w, r.WithContext(ctx))
	})
}

// uploadGuard checks and counts one receipt photo once its real size is known: file size, queue
// length, then monthly, daily and storage quotas (limits.Meter.RecordReceipt).
func (a *API) uploadGuard(p *principal) httpapi.UploadGuard {
	return func(w http.ResponseWriter, r *http.Request, size int64) (func(bool), bool) {
		ctx := r.Context()
		if err := a.Meter.CheckUpload(ctx, p.HH.ID, size); err != nil {
			a.fail(w, r, err)
			return nil, false
		}
		if queued, err := a.queuedJobs(ctx, p.HH.ID); err == nil {
			if err := a.Meter.CheckQueue(ctx, p.HH.ID, queued); err != nil {
				a.fail(w, r, err)
				return nil, false
			}
		}
		if _, err := a.Meter.RecordReceipt(ctx, p.HH.ID, size); err != nil {
			a.fail(w, r, err)
			return nil, false
		}
		return func(ok bool) {
			if !ok {
				if err := a.Meter.Release(context.WithoutCancel(ctx), p.HH.ID, size); err != nil {
					a.Log.Error("release upload quota", "err", err)
				}
			}
		}, true
	}
}

// queuedJobs counts the household's queued and running jobs.
func (a *API) queuedJobs(ctx context.Context, householdID string) (int64, error) {
	if err := a.provision(ctx, householdID); err != nil {
		return 0, err
	}
	t, release, err := a.Provider.Acquire(ctx, householdID)
	if err != nil {
		return 0, err
	}
	defer release()
	var n int64
	err = t.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running')`).Scan(&n)
	return n, err
}

// provision creates a household's folder and database on first use (idempotent, cheap when it exists).
func (a *API) provision(ctx context.Context, householdID string) error {
	if a.Provider.Exists(householdID) {
		return nil
	}
	if err := a.Provider.Provision(ctx, householdID); err != nil {
		return &httpErr{Status: http.StatusServiceUnavailable, Code: "household_unavailable", Detail: "Try again soon.",
			Header: map[string]string{"Retry-After": "5"}}
	}
	return nil
}
