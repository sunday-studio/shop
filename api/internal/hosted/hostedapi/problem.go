package hostedapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"shop/internal/hosted/control"
)

// problem is the application/problem+json body (contract 7.1).
type problem struct {
	Title             string     `json:"title"`
	Status            int        `json:"status"`
	Detail            string     `json:"detail,omitempty"`
	Code              string     `json:"code,omitempty"`
	Limit             string     `json:"limit,omitempty"`
	LimitValue        *int64     `json:"limit_value,omitempty"`
	Current           *int64     `json:"current,omitempty"`
	ResetsAt          *time.Time `json:"resets_at,omitempty"`
	PlanState         string     `json:"plan_state,omitempty"`
	DeleteAfter       *time.Time `json:"delete_after,omitempty"`
	RetryAfterSeconds *int       `json:"retry_after_seconds,omitempty"`
}

var titles = map[string]string{
	"token_invalid": "Invalid token", "apple_token_invalid": "Apple sign-in failed", "apple_nonce_invalid": "Sign-in expired",
	"admin_token_invalid": "Invalid admin token", "account_locked": "Account locked", "plan_required": "Plan required",
	"read_only": "Read-only", "feature_unavailable": "Feature unavailable", "not_household_owner": "Not the household owner",
	"not_found": "Not found", "no_household": "No household", "invite_invalid": "Invite not valid", "hosted_only": "Hosted only",
	"login_method_unavailable": "Login method unavailable", "household_full": "Household is full",
	"already_in_household": "Already in a household", "owner_cannot_leave": "The owner cannot leave",
	"cannot_remove_owner": "The owner cannot be removed", "transfer_target_no_plan": "Transfer target has no plan",
	"owner_must_confirm_household": "Confirm household deletion", "deletion_not_pending": "No deletion pending",
	"subscription_belongs_to_other_account": "Subscription belongs to another account", "export_not_ready": "Export not ready",
	"idempotency_key_reused": "Idempotency key reused", "request_in_progress": "Request in progress",
	"grant_cap_reached": "Grant cap reached", "grant_exists": "Grant exists", "export_expired": "Export expired",
	"file_too_large": "File too large", "invalid_request": "Invalid request", "apple_transaction_invalid": "Transaction not valid",
	"product_not_allowed": "Product not allowed", "limit_reached": "Limit reached", "rate_limited": "Too many requests",
	"apple_unavailable": "Apple unavailable", "household_unavailable": "Household unavailable", "internal_error": "Internal error",
}

// httpErr is an error the handlers create directly.
type httpErr struct {
	Status int
	Code   string
	Detail string
	Extra  func(*problem)
	Header map[string]string
}

func (e *httpErr) Error() string { return e.Code + ": " + e.Detail }

func newErr(status int, code, detail string) *httpErr {
	return &httpErr{Status: status, Code: code, Detail: detail}
}

func invalid(detail string) *httpErr { return newErr(http.StatusBadRequest, "invalid_request", detail) }

var errTokenInvalid = &httpErr{Status: http.StatusUnauthorized, Code: "token_invalid", Detail: "The token is missing, unknown or revoked.",
	Header: map[string]string{"WWW-Authenticate": `Bearer realm="shop", error="invalid_token"`}}

func rateLimited(retry time.Duration) *httpErr {
	secs := int(retry.Seconds() + 0.999)
	if secs < 1 {
		secs = 1
	}
	return &httpErr{Status: http.StatusTooManyRequests, Code: "rate_limited", Detail: "Slow down.",
		Extra: func(p *problem) { p.RetryAfterSeconds = &secs }, Header: map[string]string{"Retry-After": strconv.Itoa(secs)}}
}

func planGateErr(planState string, deleteAfter time.Time) *httpErr {
	if planState == "none" || planState == "deleted" {
		return &httpErr{Status: http.StatusForbidden, Code: "plan_required", Detail: "Start the free trial to use Shop.",
			Extra: func(p *problem) { p.PlanState = planState }}
	}
	return &httpErr{Status: http.StatusForbidden, Code: "read_only", Detail: "This plan is read-only. Subscribe to make changes.",
		Extra: func(p *problem) {
			p.PlanState = planState
			if !deleteAfter.IsZero() {
				t := deleteAfter.UTC()
				p.DeleteAfter = &t
			}
		}}
}

// writeJSON writes a JSON body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, p problem, headers map[string]string) {
	if p.Title == "" {
		if t, ok := titles[p.Code]; ok {
			p.Title = t
		} else {
			p.Title = http.StatusText(p.Status)
		}
	}
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// problemFor maps any error to a problem body and extra headers. unexpected is true for a 500.
func problemFor(err error) (p problem, headers map[string]string, unexpected bool) {
	var he *httpErr
	var ce *control.Error
	if errors.As(err, &he) {
		p = problem{Status: he.Status, Code: he.Code, Detail: he.Detail}
		if he.Extra != nil {
			he.Extra(&p)
		}
		return p, he.Header, false
	}
	if le, ok := control.IsLimit(err); ok {
		v, c := le.Value, le.Current
		p = problem{Status: le.HTTPStatus(), Code: le.ErrCode(), Limit: le.Limit, LimitValue: &v, Current: &c}
		if le.FileTooLarge {
			p.Detail = "That photo is larger than the maximum size."
		} else {
			p.Detail = "A limit was reached: " + le.Limit + "."
		}
		if !le.ResetsAt.IsZero() {
			t := le.ResetsAt.UTC()
			p.ResetsAt = &t
		}
		return p, nil, false
	}
	if errors.As(err, &ce) {
		p = problem{Status: ce.Status, Code: ce.Code, Detail: ce.Msg}
		headers = nil
		switch ce.Code {
		case "rate_limited":
			secs := ce.RetryAfterSeconds
			if secs < 1 {
				secs = 1
			}
			p.RetryAfterSeconds = &secs
			headers = map[string]string{"Retry-After": strconv.Itoa(secs)}
		case "apple_unavailable":
			secs := ce.RetryAfterSeconds
			if secs < 1 {
				secs = 30
			}
			p.RetryAfterSeconds = &secs
			headers = map[string]string{"Retry-After": strconv.Itoa(secs)}
		}
		return p, headers, false
	}
	return problem{Status: http.StatusInternalServerError, Code: "internal_error", Detail: "Something went wrong on our side."}, nil, true
}

// fail writes err as a problem. Unexpected errors are logged and answered 500.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	p, h, unexpected := problemFor(err)
	if unexpected {
		a.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeProblem(w, p, h)
}

// failStatus is fail with the status replaced (webhooks answer 400 where the app-facing code is 401).
func (a *API) failStatus(w http.ResponseWriter, r *http.Request, err error, status int) {
	p, h, unexpected := problemFor(err)
	if unexpected {
		a.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	} else {
		p.Status = status
	}
	writeProblem(w, p, h)
}
