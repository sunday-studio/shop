package appleauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

var t0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

const aud = "com.sundaystudi.Shop"

type env struct {
	t     *testing.T
	ctx   context.Context
	clock *hclock.Fake
	st    *control.Store
	ctl   *control.Service
	idp   *hostedtest.FakeIDP
	apple *appleauth.FakeApple
	keys  *appleauth.KeySet
	svc   *appleauth.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, ctx: context.Background(), clock: hclock.NewFake(t0), idp: hostedtest.NewFakeIDP(), apple: appleauth.NewFakeApple()}
	st, err := control.Open(filepath.Join(t.TempDir(), "c.db"), e.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	e.ctl = control.NewService(st, control.DefaultConfig(), nil)
	e.keys = &appleauth.KeySet{Fetcher: e.idp, Clock: e.clock}
	v := &appleauth.Verifier{Keys: e.keys, Clock: e.clock, Audience: aud}
	sealer, err := appleauth.NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc = appleauth.New(e.ctl, v, e.apple, e.apple, sealer)
	e.svc.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return e
}

func (e *env) claims(sub, rawNonce string, mut ...func(map[string]any)) map[string]any {
	c := map[string]any{
		"iss": appleauth.Issuer, "aud": aud, "sub": sub, "nonce": appleauth.NonceClaim(rawNonce),
		"exp": t0.Add(10 * time.Minute).Unix(), "iat": t0.Unix(), "email": sub + "@privaterelay.appleid.com",
		"email_verified": "true", "is_private_email": "true",
	}
	for _, m := range mut {
		m(c)
	}
	return c
}

// signIn runs a full request with a freshly issued nonce.
func (e *env) request(sub, kid string, mut ...func(map[string]any)) (appleauth.SignInRequest, string) {
	e.t.Helper()
	nonce, _, err := e.svc.IssueNonce(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	code := "code-" + nonce[:8]
	e.apple.AddCode(code, "refresh-"+sub)
	return appleauth.SignInRequest{
		IdentityToken: e.idp.Sign(kid, e.claims(sub, nonce, mut...)), AuthorizationCode: code, Nonce: nonce, DeviceName: "Ama's iPhone",
		GivenName: "Ama", FamilyName: "Boateng",
	}, nonce
}

func wantCode(t *testing.T, err error, want *control.Error) {
	t.Helper()
	if !control.Is(err, want) {
		t.Fatalf("got %v, want %s", err, want.Code)
	}
}

func TestIssueNonce(t *testing.T) {
	e := newEnv(t)
	n, exp, err := e.svc.IssueNonce(e.ctx)
	if err != nil || len(n) != 43 || strings.ContainsAny(n, "=+/") {
		t.Fatalf("%q %v", n, err)
	}
	if !exp.Equal(t0.Add(10 * time.Minute)) {
		t.Fatalf("expiry %v", exp)
	}
	n2, _, _ := e.svc.IssueNonce(e.ctx)
	if n == n2 {
		t.Fatal("nonces repeat")
	}
	// only the hash is stored
	if ok, _ := e.st.ConsumeNonce(e.ctx, n, t0); ok {
		t.Fatal("raw nonce must not be what is stored")
	}
	if ok, _ := e.st.ConsumeNonce(e.ctx, control.HashSecret(n), t0); !ok {
		t.Fatal("hash lookup failed")
	}
}

func TestSignInCreatesThenSignsInAgain(t *testing.T) {
	for _, kid := range []string{"rsa-1", "ec-1"} {
		t.Run(kid, func(t *testing.T) {
			e := newEnv(t)
			req, _ := e.request("sub-ama", kid)
			res, err := e.svc.SignIn(e.ctx, req)
			if err != nil || !res.Created {
				t.Fatalf("%+v %v", res, err)
			}
			if !strings.HasPrefix(res.Token, "st_") || res.Device.Name != "Ama's iPhone" || res.Account.DisplayName != "Ama B." {
				t.Fatalf("%+v", res)
			}
			if res.Account.Email != "sub-ama@privaterelay.appleid.com" || !res.Account.EmailIsPrivate {
				t.Fatalf("email: %+v", res.Account)
			}
			// the token works
			p, err := e.ctl.Authenticate(e.ctx, res.Token)
			if err != nil || p.Account.ID != res.Account.ID {
				t.Fatal(err)
			}
			// signing in never creates a household
			if _, _, err := e.st.HouseholdOfAccount(e.ctx, res.Account.ID); !control.Is(err, control.ErrNoHousehold) {
				t.Fatalf("household: %v", err)
			}
			// refresh token stored encrypted
			acc, _ := e.st.AccountByID(e.ctx, res.Account.ID)
			if len(acc.RefreshTokenEnc) == 0 || strings.Contains(string(acc.RefreshTokenEnc), "refresh-sub-ama") {
				t.Fatal("refresh token not sealed")
			}
			// second sign-in (another device, no name): same account, 200
			req2, _ := e.request("sub-ama", kid)
			req2.GivenName, req2.FamilyName, req2.DeviceName = "", "", "Ama's iPad"
			res2, err := e.svc.SignIn(e.ctx, req2)
			if err != nil || res2.Created || res2.Account.ID != res.Account.ID || res2.Account.DisplayName != "Ama B." || res2.Device.ID == res.Device.ID {
				t.Fatalf("%+v %v", res2, err)
			}
			if list, _ := e.st.ListDevices(e.ctx, res.Account.ID); len(list) != 2 {
				t.Fatalf("devices %d", len(list))
			}
		})
	}
}

func TestNonceReplayAndExpiry(t *testing.T) {
	e := newEnv(t)
	req, _ := e.request("s1", "rsa-1")
	if _, err := e.svc.SignIn(e.ctx, req); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.SignIn(e.ctx, req) // replay of the very same request
	wantCode(t, err, appleauth.ErrNonceInvalid)

	unknown, _ := e.request("s2", "rsa-1")
	unknown.Nonce = "not-a-nonce-we-issued"
	_, err = e.svc.SignIn(e.ctx, unknown)
	wantCode(t, err, appleauth.ErrNonceInvalid)

	late, _ := e.request("s3", "rsa-1")
	e.clock.Advance(11 * time.Minute)
	_, err = e.svc.SignIn(e.ctx, late)
	wantCode(t, err, appleauth.ErrNonceInvalid)
}

func TestNonceIsConsumedEvenWhenTheTokenIsBad(t *testing.T) {
	e := newEnv(t)
	req, nonce := e.request("s1", "rsa-1", func(c map[string]any) { c["aud"] = "com.evil.app" })
	_, err := e.svc.SignIn(e.ctx, req)
	wantCode(t, err, appleauth.ErrTokenInvalid)
	// the app retries with the same nonce but a good token: refused, must fetch a new nonce
	req.IdentityToken = e.idp.Sign("rsa-1", e.claims("s1", nonce))
	_, err = e.svc.SignIn(e.ctx, req)
	wantCode(t, err, appleauth.ErrNonceInvalid)
}

func TestTokenRejections(t *testing.T) {
	e := newEnv(t)
	otherRSA, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := map[string]func(req *appleauth.SignInRequest, nonce string){
		"wrong audience": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { c["aud"] = "com.other" }))
		},
		"audience list without ours": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { c["aud"] = []string{"a", "b"} }))
		},
		"wrong issuer": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { c["iss"] = "https://evil.example" }))
		},
		"expired": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { c["exp"] = t0.Add(-time.Hour).Unix() }))
		},
		"no exp": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { delete(c, "exp") }))
		},
		"issued in the future": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { c["iat"] = t0.Add(time.Hour).Unix() }))
		},
		"nonce claim for another nonce": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", "some other nonce"))
		},
		"raw nonce instead of its hash": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("s", n, func(c map[string]any) { c["nonce"] = n }))
		},
		"no subject": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.Sign("rsa-1", e.claims("", n))
		},
		"signed by a key that is not Apple's": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.SignWith("RS256", "rsa-1", otherRSA, e.claims("s", n))
		},
		"alg none": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.SignWith("none", "rsa-1", e.idp.RSAKey, e.claims("s", n))
		},
		"HS256 confusion": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.SignWith("HS256", "rsa-1", e.idp.RSAKey, e.claims("s", n))
		},
		"ES256 header on the RSA key": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.SignWith("ES256", "rsa-1", e.idp.RSAKey, e.claims("s", n))
		},
		"RS256 header on the EC key": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.SignWith("RS256", "ec-1", e.idp.ECKey, e.claims("s", n))
		},
		"unknown kid": func(r *appleauth.SignInRequest, n string) {
			r.IdentityToken = e.idp.SignWith("RS256", "rotated-away", e.idp.RSAKey, e.claims("s", n))
		},
		"tampered payload": func(r *appleauth.SignInRequest, n string) {
			p := strings.Split(r.IdentityToken, ".")
			r.IdentityToken = p[0] + "." + strings.ReplaceAll(p[1], "A", "B") + "." + p[2]
		},
		"not a jwt": func(r *appleauth.SignInRequest, n string) { r.IdentityToken = "hello" },
	}
	for name, mut := range cases {
		req, nonce := e.request("s", "rsa-1")
		mut(&req, nonce)
		_, err := e.svc.SignIn(e.ctx, req)
		if !control.Is(err, appleauth.ErrTokenInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := e.st.AccountBySub(e.ctx, "s"); !control.Is(err, control.ErrNotFound) {
		t.Fatal("an account was created from a rejected token")
	}
	// clock skew within the leeway is fine
	req, _ := e.request("skew", "rsa-1", func(c map[string]any) { c["exp"] = t0.Add(-10 * time.Second).Unix() })
	if _, err := e.svc.SignIn(e.ctx, req); err != nil {
		t.Fatalf("leeway: %v", err)
	}
	// aud as a list containing ours is fine
	req, _ = e.request("list", "ec-1", func(c map[string]any) { c["aud"] = []string{"x", aud} })
	if _, err := e.svc.SignIn(e.ctx, req); err != nil {
		t.Fatalf("aud list: %v", err)
	}
}

func TestKeyCacheAndRotation(t *testing.T) {
	e := newEnv(t)
	req, _ := e.request("s1", "rsa-1")
	e.svc.SignIn(e.ctx, req)
	req, _ = e.request("s2", "rsa-1")
	e.svc.SignIn(e.ctx, req)
	if e.idp.Fetches != 1 {
		t.Fatalf("keys fetched %d times, want 1 (cached)", e.idp.Fetches)
	}
	// a token with an unknown kid triggers one refetch ...
	req, n := e.request("s3", "rsa-1")
	req.IdentityToken = e.idp.SignWith("RS256", "brand-new", e.idp.RSAKey, e.claims("s3", n))
	e.clock.Advance(2 * time.Minute)
	e.svc.SignIn(e.ctx, req)
	if e.idp.Fetches != 2 {
		t.Fatalf("fetches %d", e.idp.Fetches)
	}
	// ... but a flood of unknown kids within a minute does not hammer Apple
	for i := 0; i < 5; i++ {
		r, n := e.request("flood", "rsa-1")
		r.IdentityToken = e.idp.SignWith("RS256", "brand-new", e.idp.RSAKey, e.claims("flood", n))
		e.svc.SignIn(e.ctx, r)
	}
	if e.idp.Fetches != 2 {
		t.Fatalf("fetches during flood %d", e.idp.Fetches)
	}
	// a rotated-in key becomes usable after the refetch window
	e.idp.RSAKid = "rsa-2"
	e.clock.Advance(2 * time.Minute)
	r, _ := e.request("s4", "rsa-2")
	if _, err := e.svc.SignIn(e.ctx, r); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	// TTL expiry refetches
	before := e.idp.Fetches
	e.clock.Advance(2 * time.Hour)
	r, _ = e.request("s5", "rsa-2")
	e.svc.SignIn(e.ctx, r)
	if e.idp.Fetches != before+1 {
		t.Fatalf("ttl refetch: %d -> %d", before, e.idp.Fetches)
	}
}

func TestAppleKeysDown(t *testing.T) {
	e := newEnv(t)
	e.idp.Down = true
	req, _ := e.request("s1", "rsa-1")
	_, err := e.svc.SignIn(e.ctx, req)
	wantCode(t, err, appleauth.ErrUnavailable)
	// after a successful fetch, a later outage keeps serving the cached keys
	e.idp.Down = false
	req, _ = e.request("s2", "rsa-1")
	if _, err := e.svc.SignIn(e.ctx, req); err != nil {
		t.Fatal(err)
	}
	e.idp.Down = true
	e.clock.Advance(3 * time.Hour) // TTL over, refresh fails, stale keys still verify
	req, _ = e.request("s3", "rsa-1", func(c map[string]any) {
		c["exp"] = t0.Add(4 * time.Hour).Unix()
		c["iat"] = t0.Add(3 * time.Hour).Unix()
	})
	if _, err := e.svc.SignIn(e.ctx, req); err != nil {
		t.Fatalf("stale cache should serve: %v", err)
	}
}

func TestCodeExchangeFailures(t *testing.T) {
	e := newEnv(t)
	req, _ := e.request("s1", "rsa-1")
	req.AuthorizationCode = "unknown-code"
	_, err := e.svc.SignIn(e.ctx, req)
	wantCode(t, err, appleauth.ErrTokenInvalid)
	req, _ = e.request("s2", "rsa-1")
	e.apple.Down = true
	_, err = e.svc.SignIn(e.ctx, req)
	wantCode(t, err, appleauth.ErrUnavailable)
	for _, sub := range []string{"s1", "s2"} {
		if _, err := e.st.AccountBySub(e.ctx, sub); !control.Is(err, control.ErrNotFound) {
			t.Fatalf("account %s created though the exchange failed", sub)
		}
	}
}

func TestLockedAndPendingDeletionAccounts(t *testing.T) {
	e := newEnv(t)
	req, _ := e.request("s1", "rsa-1")
	res, _ := e.svc.SignIn(e.ctx, req)
	// pending deletion may sign in so it can cancel
	if _, err := e.ctl.RequestAccountDeletion(e.ctx, res.Account.ID, control.DeleteConfirmText, false, res.Device.ID); err != nil {
		t.Fatal(err)
	}
	req, _ = e.request("s1", "ec-1")
	if _, err := e.svc.SignIn(e.ctx, req); err != nil {
		t.Fatalf("pending deletion sign-in: %v", err)
	}
	e.st.SetAccountStatus(e.ctx, res.Account.ID, control.AccountLocked)
	req, _ = e.request("s1", "ec-1")
	before, _ := e.st.ListDevices(e.ctx, res.Account.ID)
	_, err := e.svc.SignIn(e.ctx, req)
	wantCode(t, err, control.ErrAccountLocked)
	if after, _ := e.st.ListDevices(e.ctx, res.Account.ID); len(after) != len(before) {
		t.Fatal("a device was minted for a locked account")
	}
}

func TestRequestValidation(t *testing.T) {
	e := newEnv(t)
	good, _ := e.request("s1", "rsa-1")
	muts := []func(*appleauth.SignInRequest){
		func(r *appleauth.SignInRequest) { r.IdentityToken = "" },
		func(r *appleauth.SignInRequest) { r.AuthorizationCode = "" },
		func(r *appleauth.SignInRequest) { r.Nonce = "" },
		func(r *appleauth.SignInRequest) { r.DeviceName = "   " },
		func(r *appleauth.SignInRequest) { r.DeviceName = strings.Repeat("x", 61) },
	}
	for i, m := range muts {
		r := good
		m(&r)
		if _, err := e.svc.SignIn(e.ctx, r); control.Code(err) != "invalid_request" {
			t.Errorf("case %d: %v", i, err)
		}
	}
	// an invalid request must not burn the nonce
	if _, err := e.svc.SignIn(e.ctx, good); err != nil {
		t.Fatalf("nonce burned by a malformed request: %v", err)
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[[2]string]string{{"Ama", "Boateng"}: "Ama B.", {"Ama", ""}: "Ama", {"", "Boateng"}: "B.", {"", ""}: "", {" Kofi ", " mensah"}: "Kofi m."}
	for in, want := range cases {
		if got := appleauth.DisplayName(in[0], in[1]); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
	if got := appleauth.DisplayName(strings.Repeat("a", 80), "b"); len([]rune(got)) != 60 {
		t.Errorf("not capped: %d", len([]rune(got)))
	}
}

func TestRevocationQueueAndRetry(t *testing.T) {
	e := newEnv(t)
	req, _ := e.request("s1", "rsa-1")
	res, _ := e.svc.SignIn(e.ctx, req)
	e.apple.FailRevoke = true
	if _, err := e.ctl.RequestAccountDeletion(e.ctx, res.Account.ID, control.DeleteConfirmText, false, res.Device.ID); err != nil {
		t.Fatal(err)
	}
	ok, err := e.svc.RevokePending(e.ctx, res.Account.ID)
	if err != nil || ok {
		t.Fatalf("expected a recorded failure, got ok=%v err=%v", ok, err)
	}
	pend, _ := e.st.PendingRevocations(e.ctx)
	if len(pend) != 1 || pend[0].Attempts != 1 || pend[0].LastError == "" {
		t.Fatalf("%+v", pend)
	}
	done, failed, _ := e.svc.RetryRevocations(e.ctx)
	if done != 0 || failed != 1 {
		t.Fatalf("%d %d", done, failed)
	}
	e.apple.FailRevoke = false
	done, failed, _ = e.svc.RetryRevocations(e.ctx)
	if done != 1 || failed != 0 {
		t.Fatalf("%d %d", done, failed)
	}
	if got := e.apple.RevokedTokens(); len(got) != 1 || got[0] != "refresh-s1" {
		t.Fatalf("revoked %v", got)
	}
	if pend, _ := e.st.PendingRevocations(e.ctx); len(pend) != 0 {
		t.Fatal("still pending")
	}
	// nothing pending is "fine"
	if ok, err := e.svc.RevokePending(e.ctx, "nobody"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// revoked only once even if retried
	e.svc.RetryRevocations(e.ctx)
	if len(e.apple.RevokedTokens()) != 1 {
		t.Fatal("revoked twice")
	}
}

func (e *env) event(kid, typ, sub, jti string, extra ...func(map[string]any)) string {
	ev, _ := json.Marshal(map[string]any{"type": typ, "sub": sub, "event_time": t0.UnixMilli()})
	c := map[string]any{"iss": appleauth.Issuer, "aud": aud, "iat": t0.Unix(), "jti": jti, "events": string(ev)}
	for _, m := range extra {
		m(c)
	}
	return e.idp.Sign(kid, c)
}

func TestServerToServerEvents(t *testing.T) {
	e := newEnv(t)
	req, _ := e.request("sub-1", "rsa-1")
	res, _ := e.svc.SignIn(e.ctx, req)
	req2, _ := e.request("sub-1", "rsa-1")
	e.svc.SignIn(e.ctx, req2)
	acc := res.Account

	// email-disabled / enabled
	r, err := e.svc.HandleEvent(e.ctx, e.event("rsa-1", appleauth.EvEmailDisabled, "sub-1", "j1"))
	if err != nil || r.Outcome != "applied" || r.AccountID != acc.ID {
		t.Fatalf("%+v %v", r, err)
	}
	if a, _ := e.st.AccountByID(e.ctx, acc.ID); a.EmailDeliverable {
		t.Fatal("still deliverable")
	}
	e.svc.HandleEvent(e.ctx, e.event("rsa-1", appleauth.EvEmailEnabled, "sub-1", "j2"))
	if a, _ := e.st.AccountByID(e.ctx, acc.ID); !a.EmailDeliverable {
		t.Fatal("not re-enabled")
	}
	// duplicate jti
	r, _ = e.svc.HandleEvent(e.ctx, e.event("rsa-1", appleauth.EvEmailDisabled, "sub-1", "j1"))
	if r.Outcome != "duplicate" {
		t.Fatalf("%+v", r)
	}
	if a, _ := e.st.AccountByID(e.ctx, acc.ID); !a.EmailDeliverable {
		t.Fatal("duplicate re-applied")
	}
	// consent revoked: devices gone, account intact and can sign in again
	r, _ = e.svc.HandleEvent(e.ctx, e.event("ec-1", appleauth.EvConsentRevoked, "sub-1", "j3"))
	if r.Outcome != "applied" {
		t.Fatalf("%+v", r)
	}
	if _, err := e.ctl.Authenticate(e.ctx, res.Token); err == nil {
		t.Fatal("token still valid after consent revoked")
	}
	if a, _ := e.st.AccountByID(e.ctx, acc.ID); a.Status != control.AccountActive {
		t.Fatal("consent-revoked must not lock")
	}
	req3, _ := e.request("sub-1", "rsa-1")
	if _, err := e.svc.SignIn(e.ctx, req3); err != nil {
		t.Fatal(err)
	}
	// account-deleted: locked, devices gone, owned household enters the lapse path
	h, _, _ := e.ctl.EnsureHousehold(e.ctx, acc.ID)
	r, _ = e.svc.HandleEvent(e.ctx, e.event("rsa-1", appleauth.EvAccountDeleted, "sub-1", "j4"))
	if r.Outcome != "applied" {
		t.Fatalf("%+v", r)
	}
	a, _ := e.st.AccountByID(e.ctx, acc.ID)
	hh, _ := e.st.HouseholdByID(e.ctx, h.ID)
	if a.Status != control.AccountLocked || !hh.ForcedLapseAt.Equal(t0) || hh.Status != control.HouseholdActive {
		t.Fatalf("account %s forced lapse %v household %s", a.Status, hh.ForcedLapseAt, hh.Status)
	}
	if devs, _ := e.st.ListDevices(e.ctx, acc.ID); len(devs) != 0 {
		t.Fatal("devices remain")
	}
	req4, _ := e.request("sub-1", "rsa-1")
	_, err = e.svc.SignIn(e.ctx, req4)
	wantCode(t, err, control.ErrAccountLocked)
	// unknown account and unknown type are accepted quietly
	r, err = e.svc.HandleEvent(e.ctx, e.event("rsa-1", appleauth.EvConsentRevoked, "nobody", "j5"))
	if err != nil || r.Outcome != "unknown_account" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = e.svc.HandleEvent(e.ctx, e.event("rsa-1", "something-new", "sub-1", "j6"))
	if err != nil || r.Outcome != "ignored" {
		t.Fatalf("%+v %v", r, err)
	}
	// events as a JSON object instead of a string
	r, err = e.svc.HandleEvent(e.ctx, e.event("rsa-1", appleauth.EvEmailEnabled, "sub-1", "j7", func(c map[string]any) {
		c["events"] = map[string]any{"type": "email-enabled", "sub": "sub-1"}
	}))
	if err != nil || r.Outcome != "applied" {
		t.Fatalf("%+v %v", r, err)
	}
	// bad signature, bad audience, missing events
	_, err = e.svc.HandleEvent(e.ctx, e.idp.SignWith("RS256", "rsa-1", e.idp.RSAKey, map[string]any{"iss": appleauth.Issuer, "aud": "x", "events": "{}"}))
	wantCode(t, err, appleauth.ErrTokenInvalid)
	_, err = e.svc.HandleEvent(e.ctx, e.event("rsa-1", "x", "sub-1", "j8", func(c map[string]any) { delete(c, "events") }))
	wantCode(t, err, appleauth.ErrTokenInvalid)
	_, err = e.svc.HandleEvent(e.ctx, "garbage")
	wantCode(t, err, appleauth.ErrTokenInvalid)
}

func TestSealer(t *testing.T) {
	if _, err := appleauth.NewAESGCMSealer([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
	s, _ := appleauth.NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	a, _ := s.Seal([]byte("secret"))
	b, _ := s.Seal([]byte("secret"))
	if string(a) == string(b) {
		t.Fatal("nonce reused")
	}
	if p, err := s.Open(a); err != nil || string(p) != "secret" {
		t.Fatal(err)
	}
	a[len(a)-1] ^= 1
	if _, err := s.Open(a); err == nil {
		t.Fatal("tampering not detected")
	}
	if _, err := s.Open([]byte("x")); err == nil {
		t.Fatal("short input accepted")
	}
	s2, _ := appleauth.NewAESGCMSealer([]byte("fedcba9876543210fedcba9876543210"))
	if _, err := s2.Open(b); err == nil {
		t.Fatal("wrong key accepted")
	}
}
