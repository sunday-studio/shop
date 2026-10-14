package appleclient_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"shop/internal/hosted/appleclient"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

const bundle = "studio.sunday.shop"

type skEnv struct {
	t        *testing.T
	pki      *hostedtest.PKI
	key      *ecdsa.PrivateKey
	rec      *rec
	prod     *httptest.Server
	sandbox  *httptest.Server
	prodH    http.HandlerFunc
	sandboxH http.HandlerFunc
	mu       sync.Mutex
	reqs     []string // "ENV METHOD path?query"
	hdrs     []http.Header
	bodies   []string
	sk       *appleclient.StoreKit
}

func newSK(t *testing.T, mode appleclient.Mode) *skEnv {
	e := &skEnv{t: t, pki: hostedtest.NewPKI(t0.AddDate(-1, 0, 0)), key: hostedtest.NewKey(), rec: &rec{}}
	mk := func(env string, h *http.HandlerFunc) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			e.mu.Lock()
			e.reqs = append(e.reqs, env+" "+r.Method+" "+r.URL.RequestURI())
			e.hdrs = append(e.hdrs, r.Header.Clone())
			e.bodies = append(e.bodies, string(b))
			e.mu.Unlock()
			(*h)(w, r)
		}))
	}
	notFound := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(`{"errorCode":4040010,"errorMessage":"Transaction id not found."}`))
	}
	e.prodH, e.sandboxH = notFound, notFound
	e.prod = mk("prod", &e.prodH)
	e.sandbox = mk("sandbox", &e.sandboxH)
	t.Cleanup(e.prod.Close)
	t.Cleanup(e.sandbox.Close)
	v := &billing.Verifier{Roots: e.pki.Pool, Clock: hclock.NewFake(t0)}
	sk, err := appleclient.NewStoreKit(appleclient.StoreKitConfig{
		IssuerID: "issuer-1", KeyID: "KEY123", BundleID: bundle, Key: e.key, Mode: mode,
		ProductionURL: e.prod.URL, SandboxURL: e.sandbox.URL, Decoder: v, Clock: hclock.NewFake(t0),
		Options: e.rec.options(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.sk = sk
	return e
}

func (e *skEnv) tx(env, orig string, purchase time.Time) string {
	return e.pki.Sign(billing.TransactionInfo{
		TransactionID: orig + "-" + purchase.Format("0102"), OriginalTransactionID: orig, BundleID: bundle, ProductID: "shop.monthly",
		PurchaseDate: billing.MsOf(purchase), ExpiresDate: billing.MsOf(purchase.AddDate(0, 1, 0)), Environment: env,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (e *skEnv) statusHandler(env, orig string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"environment": env, "bundleId": bundle,
			"data": []any{map[string]any{"subscriptionGroupIdentifier": "g1", "lastTransactions": []any{map[string]any{
				"status": 4, "originalTransactionId": orig,
				"signedTransactionInfo": e.tx(env, orig, t0.AddDate(0, 0, -3)),
				"signedRenewalInfo":     e.pki.Sign(billing.RenewalInfo{OriginalTransactionID: orig, AutoRenewStatus: 1, Environment: env}),
			}}}},
		})
	}
}

func TestStatusRequestShapeAndDecoding(t *testing.T) {
	e := newSK(t, appleclient.Production)
	e.prodH = e.statusHandler("Production", "1000")
	st, err := e.sk.GetSubscriptionStatus(t.Context(), "1000")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != 4 || st.Transaction.OriginalTransactionID != "1000" || st.Environment != "Production" || st.Renewal.AutoRenewStatus != 1 {
		t.Fatalf("bad status: %+v", st)
	}
	if len(e.reqs) != 1 || e.reqs[0] != "prod GET /inApps/v1/subscriptions/1000" {
		t.Fatalf("requests: %v", e.reqs)
	}
	auth := e.hdrs[0].Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || e.hdrs[0].Get("Accept") != "application/json" {
		t.Fatalf("headers: %v", e.hdrs[0])
	}
	hdr, claims := parseJWT(t, strings.TrimPrefix(auth, "Bearer "), &e.key.PublicKey)
	if hdr["alg"] != "ES256" || hdr["kid"] != "KEY123" || hdr["typ"] != "JWT" {
		t.Errorf("header: %v", hdr)
	}
	if claims["iss"] != "issuer-1" || claims["bid"] != bundle || claims["aud"] != "appstoreconnect-v1" {
		t.Errorf("claims: %v", claims)
	}
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	if iat != t0.Unix() || exp-iat != 300 {
		t.Errorf("iat %d exp %d", iat, exp)
	}
}

func TestAutoTriesProductionThenSandbox(t *testing.T) {
	e := newSK(t, appleclient.Auto)
	e.sandboxH = e.statusHandler("Sandbox", "2000")
	st, err := e.sk.GetSubscriptionStatus(t.Context(), "2000")
	if err != nil || st.Environment != "Sandbox" {
		t.Fatalf("%v %+v", err, st)
	}
	if len(e.reqs) != 2 || !strings.HasPrefix(e.reqs[0], "prod ") || !strings.HasPrefix(e.reqs[1], "sandbox ") {
		t.Fatalf("order: %v", e.reqs)
	}
	// production knows it: sandbox is not asked
	e2 := newSK(t, appleclient.Auto)
	e2.prodH = e2.statusHandler("Production", "3000")
	if _, err := e2.sk.GetSubscriptionStatus(t.Context(), "3000"); err != nil || len(e2.reqs) != 1 {
		t.Fatalf("%v %v", err, e2.reqs)
	}
	// unknown everywhere
	e3 := newSK(t, appleclient.Auto)
	if _, err := e3.sk.GetSubscriptionStatus(t.Context(), "9"); !errors.Is(err, billing.ErrAppleNotFound) || len(e3.reqs) != 2 {
		t.Fatalf("%v %v", err, e3.reqs)
	}
	// production-only and sandbox-only modes
	e4 := newSK(t, appleclient.Production)
	if _, err := e4.sk.GetSubscriptionStatus(t.Context(), "9"); !errors.Is(err, billing.ErrAppleNotFound) || len(e4.reqs) != 1 || !strings.HasPrefix(e4.reqs[0], "prod") {
		t.Fatalf("%v %v", err, e4.reqs)
	}
	e5 := newSK(t, appleclient.Sandbox)
	e5.sandboxH = e5.statusHandler("Sandbox", "5")
	if _, err := e5.sk.GetSubscriptionStatus(t.Context(), "5"); err != nil || len(e5.reqs) != 1 || !strings.HasPrefix(e5.reqs[0], "sandbox") {
		t.Fatalf("%v %v", err, e5.reqs)
	}
}

func TestRetriesAndErrorMapping(t *testing.T) {
	e := newSK(t, appleclient.Production)
	n := 0
	e.prodH = func(w http.ResponseWriter, r *http.Request) {
		n++
		switch n {
		case 1:
			writeJSON(w, 500, map[string]any{"errorCode": 5000000, "errorMessage": "boom"})
		case 2:
			w.Header().Set("Retry-After", "2")
			writeJSON(w, 429, map[string]any{"errorCode": 4290000, "errorMessage": "slow down"})
		default:
			e.statusHandler("Production", "1")(w, r)
		}
	}
	if _, err := e.sk.GetSubscriptionStatus(t.Context(), "1"); err != nil {
		t.Fatalf("should succeed after retries: %v", err)
	}
	if n != 3 || len(e.rec.Sleeps) != 2 {
		t.Fatalf("attempts %d sleeps %v", n, e.rec.Sleeps)
	}
	if e.rec.Sleeps[1] < 2*time.Second {
		t.Errorf("Retry-After not honoured: %v", e.rec.Sleeps)
	}
	// a JWT is signed per attempt (fresh request each time)
	if len(e.hdrs) != 3 {
		t.Fatalf("requests: %d", len(e.hdrs))
	}

	// persistent 503: all attempts used, unavailable
	e = newSK(t, appleclient.Production)
	e.prodH = func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 503, map[string]any{}) }
	_, err := e.sk.GetSubscriptionStatus(t.Context(), "1")
	var ae *appleclient.APIError
	if !errors.Is(err, billing.ErrAppleUnavailable) || !errors.As(err, &ae) || ae.Status != 503 || len(e.reqs) != 3 {
		t.Fatalf("%v (%d requests)", err, len(e.reqs))
	}
	// 401 (bad key): unavailable, not retried, Apple's message passed through
	e = newSK(t, appleclient.Production)
	e.prodH = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 401, map[string]any{"errorCode": 4010001, "errorMessage": "Authentication failed"})
	}
	_, err = e.sk.GetSubscriptionStatus(t.Context(), "1")
	if !errors.Is(err, billing.ErrAppleUnavailable) || len(e.reqs) != 1 || !strings.Contains(err.Error(), "4010001") {
		t.Fatalf("%v", err)
	}
	// 400 is neither unavailable nor not-found
	e = newSK(t, appleclient.Production)
	e.prodH = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 400, map[string]any{"errorCode": 4000006, "errorMessage": "Invalid transaction id."})
	}
	_, err = e.sk.GetSubscriptionStatus(t.Context(), "x")
	if errors.Is(err, billing.ErrAppleUnavailable) || errors.Is(err, billing.ErrAppleNotFound) || err == nil {
		t.Fatalf("%v", err)
	}
	// connection refused: unavailable
	e = newSK(t, appleclient.Production)
	e.prod.Close()
	if _, err = e.sk.GetSubscriptionStatus(t.Context(), "1"); !errors.Is(err, billing.ErrAppleUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestUnverifiedOrForeignValuesRejected(t *testing.T) {
	// answer signed by an attacker's CA
	e := newSK(t, appleclient.Production)
	attacker := hostedtest.OtherPKI(t0.AddDate(-1, 0, 0))
	e.prodH = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"bundleId": bundle, "data": []any{map[string]any{"lastTransactions": []any{map[string]any{
			"status": 1, "originalTransactionId": "1",
			"signedTransactionInfo": attacker.Sign(billing.TransactionInfo{OriginalTransactionID: "1", BundleID: bundle}),
		}}}}})
	}
	if _, err := e.sk.GetSubscriptionStatus(t.Context(), "1"); !errors.Is(err, billing.ErrAppleUnavailable) {
		t.Fatalf("forged answer accepted: %v", err)
	}
	// another app's transaction, wrong environment, other subscription
	for name, tx := range map[string]billing.TransactionInfo{
		"other bundle": {OriginalTransactionID: "1", BundleID: "evil.app", Environment: "Production"},
		"wrong env":    {OriginalTransactionID: "1", BundleID: bundle, Environment: "Sandbox"},
		"other sub":    {OriginalTransactionID: "2", BundleID: bundle, Environment: "Production"},
	} {
		e := newSK(t, appleclient.Production)
		signed := e.pki.Sign(tx)
		e.prodH = func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"bundleId": bundle, "data": []any{map[string]any{"lastTransactions": []any{map[string]any{
				"status": 1, "originalTransactionId": "1", "signedTransactionInfo": signed}}}}})
		}
		if _, err := e.sk.GetSubscriptionStatus(t.Context(), "1"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestHistoryPaginationAndOrder(t *testing.T) {
	e := newSK(t, appleclient.Production)
	d := func(n int) time.Time { return t0.AddDate(0, -n, 0) }
	e.prodH = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("revision") == "" {
			writeJSON(w, 200, map[string]any{"revision": "rev1", "hasMore": true, "signedTransactions": []string{e.tx("Production", "7", d(1)), e.tx("Production", "7", d(2))}})
			return
		}
		writeJSON(w, 200, map[string]any{"revision": "rev2", "hasMore": false, "signedTransactions": []string{e.tx("Production", "7", d(3))}})
	}
	h, err := e.sk.GetTransactionHistory(t.Context(), "7")
	if err != nil || len(h) != 3 {
		t.Fatalf("%v %d", err, len(h))
	}
	if !(h[0].PurchaseDate < h[1].PurchaseDate && h[1].PurchaseDate < h[2].PurchaseDate) {
		t.Errorf("not oldest first: %v", h)
	}
	if e.reqs[0] != "prod GET /inApps/v2/history/7?sort=ASCENDING" || e.reqs[1] != "prod GET /inApps/v2/history/7?revision=rev1&sort=ASCENDING" {
		t.Errorf("requests: %v", e.reqs)
	}
}

func TestSetAppAccountToken(t *testing.T) {
	e := newSK(t, appleclient.Production)
	e.prodH = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }
	if err := e.sk.SetAppAccountToken(t.Context(), "55", "6f9619ff-8b86-d011-b42d-00c04fc964ff"); err != nil {
		t.Fatal(err)
	}
	if e.reqs[0] != "prod PUT /inApps/v1/transactions/55/appAccountToken" {
		t.Errorf("%v", e.reqs)
	}
	if e.bodies[0] != `{"appAccountToken":"6f9619ff-8b86-d011-b42d-00c04fc964ff"}` || e.hdrs[0].Get("Content-Type") != "application/json" {
		t.Errorf("body %q %v", e.bodies[0], e.hdrs[0])
	}
}

func TestTestNotificationAndHistory(t *testing.T) {
	e := newSK(t, appleclient.Auto)
	e.sandboxH = func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/inApps/v1/notifications/test":
			writeJSON(w, 200, map[string]any{"testNotificationToken": "tok-1"})
		case r.URL.Path == "/inApps/v1/notifications/test/tok-1":
			writeJSON(w, 200, map[string]any{"signedPayload": "x.y.z", "sendAttempts": []any{map[string]any{"attemptDate": 5, "sendAttemptResult": "SUCCESS"}}})
		case r.URL.Path == "/inApps/v1/notifications/history":
			writeJSON(w, 200, map[string]any{"notificationHistory": []any{map[string]any{"signedPayload": "a.b.c"}}, "hasMore": true, "paginationToken": "p2"})
		}
	}
	tok, err := e.sk.RequestTestNotification(t.Context(), billing.EnvSandbox)
	if err != nil || tok != "tok-1" {
		t.Fatalf("%v %q", err, tok)
	}
	res, err := e.sk.GetTestNotificationStatus(t.Context(), billing.EnvSandbox, tok)
	if err != nil || res.SignedPayload != "x.y.z" || res.SendAttempts[0].SendAttemptResult != "SUCCESS" {
		t.Fatalf("%v %+v", err, res)
	}
	items, next, more, err := e.sk.GetNotificationHistory(t.Context(), billing.EnvSandbox,
		appleclient.HistoryRequest{StartDate: billing.MsOf(t0.AddDate(0, 0, -7)), EndDate: billing.MsOf(t0), OnlyFailures: true}, "p1")
	if err != nil || len(items) != 1 || next != "p2" || !more {
		t.Fatalf("%v %v %q %v", err, items, next, more)
	}
	last := len(e.reqs) - 1
	if e.reqs[last] != "sandbox POST /inApps/v1/notifications/history?paginationToken=p1" {
		t.Errorf("%v", e.reqs)
	}
	var body map[string]any
	if json.Unmarshal([]byte(e.bodies[last]), &body) != nil || body["onlyFailures"] != true || body["startDate"] != float64(t0.AddDate(0, 0, -7).UnixMilli()) {
		t.Errorf("body %s", e.bodies[last])
	}
	for _, r := range e.reqs { // explicit environment: production never asked
		if strings.HasPrefix(r, "prod") {
			t.Errorf("production asked: %v", e.reqs)
		}
	}
	if _, err := e.sk.RequestTestNotification(t.Context(), "Xcode"); err == nil {
		t.Error("unknown environment accepted")
	}
}

func TestNoSecretsInLogsOrErrors(t *testing.T) {
	e := newSK(t, appleclient.Production)
	e.prodH = func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 500, map[string]any{"errorMessage": "x"}) }
	_, err := e.sk.GetSubscriptionStatus(t.Context(), "1")
	jwtTok := strings.TrimPrefix(e.hdrs[0].Get("Authorization"), "Bearer ")
	for _, s := range []string{e.rec.Logs.String(), err.Error()} {
		if strings.Contains(s, jwtTok) || strings.Contains(s, "Bearer") || strings.Contains(s, e.key.D.Text(16)) {
			t.Errorf("secret leaked: %s", s)
		}
	}
	if e.rec.Logs.Len() == 0 {
		t.Error("expected some log output")
	}
}

func TestNewStoreKitValidates(t *testing.T) {
	if _, err := appleclient.NewStoreKit(appleclient.StoreKitConfig{}); err == nil {
		t.Error("empty config accepted")
	}
}
