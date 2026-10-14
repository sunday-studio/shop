// Package hostedworld builds a complete hosted server in memory for tests: the control database,
// every hosted service with a fake clock and fake Apple, the tenant provider on a temporary
// folder, and the HTTP API. Test code only.
package hostedworld

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/config"
	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedapi"
	"shop/internal/hosted/hostedtest"
	"shop/internal/hosted/lifecycle"
	"shop/internal/hosted/limits"
	"shop/internal/tenancy"
	"shop/internal/tenantapp"
)

const (
	Bundle  = "com.sundaystudi.Shop"
	Product = "com.sundaystudi.shop.hosted.monthly"
	Admin   = "sa_testadmintoken"
)

// T0 is the start of the fake clock.
var T0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

// Day is T0 plus n days.
func Day(n int) time.Time { return T0.Add(time.Duration(n) * 24 * time.Hour) }

// World is one hosted server.
type World struct {
	T       testing.TB
	Ctx     context.Context
	Clock   *hclock.Fake
	Store   *control.Store
	Ctl     *control.Service
	Calc    *billing.Calculator
	Bill    *billing.Service
	Auth    *appleauth.Service
	Grants  *grants.Service
	Meter   *limits.Meter
	Life    *lifecycle.Runner
	IDP     *hostedtest.FakeIDP
	Apple   *appleauth.FakeApple
	API     *billing.FakeAPI
	PKI     *hostedtest.PKI
	Sink    *lifecycle.MemorySink
	Prov    *tenancy.Multi
	RT      *tenantapp.Runtime
	Hosted  *hostedapi.API
	Handler http.Handler
	Admin   http.Handler
	Dir     string
	n       int
}

// Option changes the setup.
type Option func(*hostedapi.Config)

// New builds a world. Rate limits are on (as in production) unless NoRates is given.
func New(t testing.TB, opts ...Option) *World { return NewWith(t, "", opts...) }

// NewWith is New with the URL of the OCR sidecar the households use ("" = a stub that is never ready).
func NewWith(t testing.TB, ocrURL string, opts ...Option) *World {
	t.Helper()
	w := &World{T: t, Ctx: context.Background(), Clock: hclock.NewFake(T0), IDP: hostedtest.NewFakeIDP(), Apple: appleauth.NewFakeApple(),
		API: billing.NewFakeAPI(), Sink: &lifecycle.MemorySink{}, Dir: t.TempDir()}
	w.PKI = hostedtest.NewPKI(T0.AddDate(-1, 0, 0))
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := control.Open(filepath.Join(w.Dir, "control.db"), w.Clock)
	if err != nil {
		t.Fatal(err)
	}
	w.Store = st
	t.Cleanup(func() { st.Close() })
	cfg := hostedapi.DefaultConfig()
	cfg.ExportDir = filepath.Join(w.Dir, "exports")
	cfg.ProductIDs = []string{Product}
	for _, o := range opts {
		o(&cfg)
	}
	w.Calc = billing.NewCalculator(st)
	w.Ctl = control.NewService(st, cfg.Limits.ControlConfig(control.DefaultConfig()), w.Calc.AccessChecker())
	v := &billing.Verifier{Roots: w.PKI.Pool, Clock: w.Clock, LeafOID: hostedtest.LeafOID, IntermediateOID: hostedtest.IntermediateOID}
	w.Bill = billing.NewService(w.Ctl, v, w.API, billing.Config{BundleID: Bundle, ProductIDs: []string{Product}})
	w.Bill.Log = quiet
	sealer, _ := appleauth.NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	keys := &appleauth.KeySet{Fetcher: w.IDP, Clock: w.Clock}
	w.Auth = appleauth.New(w.Ctl, &appleauth.Verifier{Keys: keys, Clock: w.Clock, Audience: Bundle}, w.Apple, w.Apple, sealer)
	w.Auth.Log = quiet
	w.Grants = grants.New(w.Ctl, grants.DefaultConfig())
	w.Meter = limits.NewMeter(st, cfg.Limits)

	if ocrURL == "" {
		ocrSrv := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(ocrSrv.Close)
		ocrURL = ocrSrv.URL
	}
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	rt := tenantapp.NewRuntime(config.Config{DataDir: w.Dir, OCRURL: ocrURL, FoodFactsUserAgent: "test", Location: ams, Workers: 1, AutoProducts: true}, quiet)
	w.RT = rt
	prov, err := tenancy.NewMulti(tenancy.MultiConfig{Root: filepath.Join(w.Dir, "data"), BackupRoot: filepath.Join(w.Dir, "backup"), MaxOpen: 16, Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	w.Prov = prov
	t.Cleanup(func() { prov.Close() })
	api, err := hostedapi.New(hostedapi.Deps{Cfg: cfg, Clock: w.Clock, Log: quiet, Ctl: w.Ctl, Calc: w.Calc, Bill: w.Bill, Auth: w.Auth,
		Grants: w.Grants, Meter: w.Meter, Provider: prov, Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	w.Hosted = api
	w.Handler = api.Handler()
	w.Admin = api.AdminHandler(hostedapi.HashAdminToken(Admin))
	w.Life = &lifecycle.Runner{S: st, Ctl: w.Ctl, Calc: w.Calc, Apple: w.Bill, Revoker: w.Auth,
		Eraser: hostedapi.Eraser{Provider: prov, OS: lifecycle.OSEraser{Layout: w.Layout()}},
		Layout: w.Layout(), Sink: w.Sink, Cfg: lifecycle.DefaultConfig(), Log: quiet}
	return w
}

// NoRates turns the rate limits off.
func NoRates(c *hostedapi.Config) { c.Rates.Disabled = true }

// Layout is where the households' files are.
func (w *World) Layout() lifecycle.Layout {
	return lifecycle.Layout{DataDir: filepath.Join(w.Dir, "data"), BackupDir: filepath.Join(w.Dir, "backup"), ExportDir: filepath.Join(w.Dir, "exports")}
}

// At moves the clock to day n.
func (w *World) At(n int) { w.Clock.Set(Day(n)) }

// Resp is a recorded response.
type Resp struct {
	*httptest.ResponseRecorder
}

// JSON decodes the body as an object.
func (r Resp) JSON() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &m)
	return m
}

// Code returns problem.code ("" when none).
func (r Resp) Code() string {
	s, _ := r.JSON()["code"].(string)
	return s
}

// Do sends a request to the public handler. body may be nil, []byte, or anything JSON-encodable.
// headers are name, value pairs.
func (w *World) Do(method, path, token string, body any, headers ...string) Resp {
	return w.do(w.Handler, method, path, token, body, "198.51.100.7:4000", headers...)
}

// DoFrom is Do from a given remote address.
func (w *World) DoFrom(remote, method, path, token string, body any, headers ...string) Resp {
	return w.do(w.Handler, method, path, token, body, remote, headers...)
}

// DoAdmin sends a request to the admin handler from loopback with the admin token.
func (w *World) DoAdmin(method, path string, body any, headers ...string) Resp {
	h := append([]string{"X-Admin-Operator", "casprine"}, headers...)
	return w.do(w.Admin, method, path, Admin, body, "127.0.0.1:5555", h...)
}

// DoRaw sends to the admin handler with full control of the remote address and token.
func (w *World) DoAdminRaw(remote, token, method, path string, body any, headers ...string) Resp {
	return w.do(w.Admin, method, path, token, body, remote, headers...)
}

func (w *World) do(h http.Handler, method, path, token string, body any, remote string, headers ...string) Resp {
	w.T.Helper()
	var rd io.Reader
	ct := ""
	switch v := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(v)
	default:
		b, _ := json.Marshal(v)
		rd, ct = bytes.NewReader(b), "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return Resp{rec}
}

// User is a signed-in account.
type User struct {
	Token     string
	AccountID string
	DeviceID  int64
	Account   map[string]any
	Sub       string
}

// IdentityToken signs an Apple identity token for sub with the given raw nonce.
func (w *World) IdentityToken(sub, nonce string) string {
	return w.IDP.Sign("rsa-1", map[string]any{"iss": appleauth.Issuer, "aud": Bundle, "sub": sub, "nonce": appleauth.NonceClaim(nonce),
		"exp": w.Clock.Now().Add(10 * time.Minute).Unix(), "iat": w.Clock.Now().Unix(), "email": sub + "@privaterelay.appleid.com", "is_private_email": "true"})
}

// Nonce fetches a nonce over HTTP.
func (w *World) Nonce() string {
	w.T.Helper()
	r := w.Do("POST", "/v1/auth/apple/nonce", "", nil)
	if r.Status() != 201 {
		w.T.Fatalf("nonce: %d %s", r.Status(), r.Body)
	}
	return r.JSON()["nonce"].(string)
}

// Status is the HTTP status (Code is the problem code).
func (r Resp) Status() int { return r.ResponseRecorder.Code }

// SignIn runs the whole sign-in over HTTP.
func (w *World) SignIn(sub, name string) User { return w.SignInDevice(sub, name, name+"'s iPhone") }

// SignInDevice is SignIn with a chosen device name.
func (w *World) SignInDevice(sub, name, device string) User {
	w.T.Helper()
	nonce := w.Nonce()
	w.n++
	code := "code-" + sub + "-" + nonce[:6]
	w.Apple.AddCode(code, "refresh-"+sub)
	r := w.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": w.IdentityToken(sub, nonce), "authorization_code": code, "nonce": nonce,
		"device_name": device, "name": map[string]any{"given_name": name, "family_name": "Test"}})
	if r.Status() != 200 && r.Status() != 201 {
		w.T.Fatalf("sign in %s: %d %s", sub, r.Status(), r.Body)
	}
	j := r.JSON()
	acc := j["account"].(map[string]any)
	return User{Token: j["token"].(string), AccountID: acc["id"].(string), DeviceID: int64(j["device"].(map[string]any)["id"].(float64)), Account: acc, Sub: sub}
}

// Tx builds a transaction for the user (a 14 day free trial unless mut changes it).
func (w *World) Tx(u User, otx string, mut ...func(*billing.TransactionInfo)) billing.TransactionInfo {
	t := billing.TransactionInfo{TransactionID: otx + "-x", OriginalTransactionID: otx, BundleID: Bundle, ProductID: Product,
		OriginalPurchaseDate: billing.MsOf(T0), PurchaseDate: billing.MsOf(w.Clock.Now()), ExpiresDate: billing.MsOf(Day(14)), Type: "Auto-Renewable Subscription",
		AppAccountToken: u.Account["app_account_token"].(string), Environment: "Sandbox", OfferType: 1, OfferDiscountType: "FREE_TRIAL", SignedDate: billing.MsOf(w.Clock.Now())}
	for _, m := range mut {
		m(&t)
	}
	return t
}

// Ren builds renewal info.
func Ren(autoRenew int) *billing.RenewalInfo {
	return &billing.RenewalInfo{AutoRenewStatus: autoRenew, AutoRenewProductID: Product}
}

// SetStatus defines what the fake Apple server says.
func (w *World) SetStatus(otx string, code int, tx billing.TransactionInfo) {
	w.API.SetStatus(otx, billing.SubscriptionStatus{Status: code, Transaction: tx, Renewal: *Ren(1), Environment: "Sandbox"})
}

// Subscribe starts the trial for u over HTTP (Apple says "active").
func (w *World) Subscribe(u User, otx string) billing.TransactionInfo {
	w.T.Helper()
	tx := w.Tx(u, otx)
	w.SetStatus(otx, 1, tx)
	r := w.Do("POST", "/v1/billing/apple/transactions", u.Token, map[string]any{"signed_transaction": w.PKI.Sign(tx)})
	if r.Status() != 200 {
		w.T.Fatalf("subscribe: %d %s", r.Status(), r.Body)
	}
	return tx
}

// Notification signs an App Store notification.
func (w *World) Notification(typ, sub, uuid string, signed time.Time, tx billing.TransactionInfo, ren *billing.RenewalInfo) string {
	data := billing.NotificationData{BundleID: Bundle, Environment: "Sandbox", SignedTransactionInfo: w.PKI.Sign(tx)}
	if ren != nil {
		data.SignedRenewalInfo = w.PKI.Sign(ren)
	}
	return w.PKI.Sign(billing.NotificationPayload{NotificationType: typ, Subtype: sub, NotificationUUID: uuid, Version: "2.0", SignedDate: billing.MsOf(signed), Data: data})
}

// Deliver posts a signed notification to the webhook.
func (w *World) Deliver(signed string) Resp {
	return w.Do("POST", "/v1/billing/apple/notifications", "", map[string]any{"signedPayload": signed})
}

// PlanState reads the user's plan state over HTTP.
func (w *World) PlanState(u User) string {
	w.T.Helper()
	r := w.Do("GET", "/v1/account", u.Token, nil)
	if r.Status() != 200 {
		w.T.Fatalf("account: %d %s", r.Status(), r.Body)
	}
	return r.JSON()["plan"].(map[string]any)["state"].(string)
}

// HouseholdID returns the user's household id ("" when none).
func (w *World) HouseholdID(u User) string {
	h, _, err := w.Store.HouseholdOfAccount(w.Ctx, u.AccountID)
	if err != nil {
		return ""
	}
	return h.ID
}
