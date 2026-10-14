package hostedapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shop/internal/hosted/admin"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
)

// HashAdminToken is the hash stored in ADMIN_TOKEN_HASH for an admin token "sa_...".
func HashAdminToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// NewAdminToken makes an admin token and its hash.
func NewAdminToken() (token, hash string) {
	t, _ := control.NewToken()
	token = "sa_" + strings.TrimPrefix(t, "st_")
	return token, HashAdminToken(token)
}

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AdminHandler serves /admin/v1/* for the separate loopback listener. Every request must come from
// a loopback address and carry the admin token (compared in constant time) and X-Admin-Operator.
// A device token never works here and the admin token never works on /v1 (a different listener and
// a different token space). An empty tokenHash disables the admin API (every request is refused).
func (a *API) AdminHandler(tokenHash string) http.Handler {
	env := admin.Env{Ctl: a.Ctl, Grants: a.Grants, Calc: a.Calc}
	mux := http.NewServeMux()
	type h func(w http.ResponseWriter, r *http.Request, operator string)
	route := func(pattern string, fn h) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if !isLoopback(r.RemoteAddr) {
				a.fail(w, r, control.ErrNotFound)
				return
			}
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			sum := HashAdminToken(strings.TrimSpace(raw))
			if !ok || tokenHash == "" || subtle.ConstantTimeCompare([]byte(sum), []byte(strings.ToLower(tokenHash))) != 1 {
				a.fail(w, r, &httpErr{Status: http.StatusUnauthorized, Code: "admin_token_invalid", Detail: "The admin token is missing or wrong."})
				return
			}
			op := strings.TrimSpace(r.Header.Get("X-Admin-Operator"))
			if op == "" || len(op) > 40 {
				a.fail(w, r, invalid("X-Admin-Operator (1 to 40 characters) is required"))
				return
			}
			fn(w, r, op)
		})
	}
	route("GET /admin/v1/accounts", func(w http.ResponseWriter, r *http.Request, op string) {
		q := r.URL.Query()
		var found []control.Account
		var err error
		switch {
		case q.Get("support_code") != "":
			var acc control.Account
			if acc, err = a.Store.AccountBySupportCode(r.Context(), q.Get("support_code")); err == nil {
				found = []control.Account{acc}
			}
		case q.Get("account_id") != "":
			var acc control.Account
			if acc, err = a.Store.AccountByID(r.Context(), q.Get("account_id")); err == nil {
				found = []control.Account{acc}
			}
		case q.Get("email") != "":
			found, err = a.Store.AccountsByEmail(r.Context(), q.Get("email"))
		default:
			err = invalid("give support_code, account_id or email")
		}
		if err != nil && !control.Is(err, control.ErrNotFound) {
			a.fail(w, r, err)
			return
		}
		out := make([]admin.AccountJSON, 0, len(found))
		for _, acc := range found {
			row := admin.AccountJSON{ID: acc.ID, SupportCode: acc.SupportCode, Email: acc.Email, DisplayName: acc.DisplayName, CreatedAt: acc.CreatedAt}
			if pl, perr := a.Calc.ForAccount(r.Context(), acc.ID); perr == nil {
				row.PlanState = pl.State
			}
			if hh, _, herr := a.Store.HouseholdOfAccount(r.Context(), acc.ID); herr == nil {
				id := hh.ID
				row.HouseholdID = &id
			}
			out = append(out, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
	})
	route("POST /admin/v1/grants", func(w http.ResponseWriter, r *http.Request, op string) {
		var in struct {
			AccountID   string     `json:"account_id"`
			SupportCode string     `json:"support_code"`
			Key         string     `json:"key"`
			ExpiresAt   *time.Time `json:"expires_at"`
			Days        int        `json:"days"`
			Reason      string     `json:"reason"`
		}
		if err := decodeJSON(r, &in, false); err != nil {
			a.fail(w, r, err)
			return
		}
		idem := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idem == "" || len(idem) > 100 {
			a.fail(w, r, invalid("Idempotency-Key is required (at most 100 characters)"))
			return
		}
		ci := grants.CreateInput{AccountID: in.AccountID, SupportCode: in.SupportCode, Key: in.Key, Days: in.Days, Reason: in.Reason, Actor: op, IdempotencyKey: idem}
		if in.ExpiresAt != nil {
			ci.ExpiresAt = in.ExpiresAt.UTC()
		}
		g, replay, err := a.Grants.Create(r.Context(), ci)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		status := http.StatusCreated
		if replay {
			status = http.StatusOK
		}
		writeJSON(w, status, env.GrantJSON(r.Context(), g))
	})
	route("GET /admin/v1/grants", func(w http.ResponseWriter, r *http.Request, op string) {
		q := r.URL.Query()
		st := q.Get("status")
		switch st {
		case "", "active", "expired", "revoked":
		default:
			a.fail(w, r, invalid("status must be active, expired or revoked"))
			return
		}
		gs, err := a.Grants.List(r.Context(), grants.ListFilter{Status: grants.Status(st), AccountID: q.Get("account_id")})
		if err != nil {
			a.fail(w, r, err)
			return
		}
		out := make([]admin.GrantJSON, 0, len(gs))
		for _, g := range gs {
			out = append(out, env.GrantJSON(r.Context(), g))
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": out})
	})
	route("POST /admin/v1/grants/{id}/extend", func(w http.ResponseWriter, r *http.Request, op string) {
		var in struct {
			ExpiresAt time.Time `json:"expires_at"`
			Reason    string    `json:"reason"`
		}
		if err := decodeJSON(r, &in, false); err != nil || in.ExpiresAt.IsZero() {
			a.fail(w, r, invalid("expires_at and reason are required"))
			return
		}
		g, err := a.Grants.Extend(r.Context(), r.PathValue("id"), in.ExpiresAt, in.Reason, op)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, env.GrantJSON(r.Context(), g))
	})
	route("POST /admin/v1/grants/{id}/revoke", func(w http.ResponseWriter, r *http.Request, op string) {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &in, false); err != nil {
			a.fail(w, r, err)
			return
		}
		g, err := a.Grants.Revoke(r.Context(), r.PathValue("id"), in.Reason, op)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, env.GrantJSON(r.Context(), g))
	})
	route("GET /admin/v1/audit", func(w http.ResponseWriter, r *http.Request, op string) {
		q := r.URL.Query()
		limit := 50
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 500 {
				a.fail(w, r, invalid("limit must be 1 to 500"))
				return
			}
			limit = n
		}
		es, err := a.Store.ListAudit(r.Context(), control.AuditFilter{Subject: q.Get("subject"), Action: q.Get("action"), Limit: limit})
		if err != nil {
			a.fail(w, r, err)
			return
		}
		type row struct {
			ID      int64          `json:"id"`
			At      time.Time      `json:"at"`
			Actor   string         `json:"actor"`
			Action  string         `json:"action"`
			Subject string         `json:"subject"`
			Details map[string]any `json:"details"`
		}
		out := make([]row, 0, len(es))
		for _, e := range es {
			d := map[string]any{}
			_ = jsonUnmarshal(e.Details, &d)
			out = append(out, row{e.ID, e.At, e.Actor, e.Action, e.Subject, d})
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": out})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			a.fail(w, r, control.ErrNotFound)
			return
		}
		a.fail(w, r, control.ErrNotFound)
	})
	return a.logged(mux)
}
