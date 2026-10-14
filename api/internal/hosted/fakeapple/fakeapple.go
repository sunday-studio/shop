// Package fakeapple is a stand-in for Apple for local stacks and end-to-end tests: Sign in with
// Apple keys and token exchange, the App Store Server API and signed notifications, all in
// process with a throw-away certificate chain. The hosted binary uses it only with
// SHOP_APPLE_MODE=fake (never with the production environment) and exposes its helpers as
// development routes on the admin listener (/admin/v1/dev/...), so a developer or a test can
// mint identity tokens, signed transactions and signed notifications for the running server.
package fakeapple

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

// Server holds the fakes. Wire IDP (KeyFetcher), Apple (CodeExchanger, TokenRevoker), API
// (billing.ServerAPI) and Verifier into the services.
type Server struct {
	IDP      *hostedtest.FakeIDP
	Apple    *appleauth.FakeApple
	API      *billing.FakeAPI
	PKI      *hostedtest.PKI
	Clock    hclock.Clock
	Bundle   string
	Product  string
	Env      string // "Sandbox" (default) or "Production"
	verifier *billing.Verifier
}

// New builds a fake Apple.
func New(clock hclock.Clock, bundle, product string) *Server {
	if clock == nil {
		clock = hclock.System{}
	}
	now := clock.Now()
	s := &Server{IDP: hostedtest.NewFakeIDP(), Apple: appleauth.NewFakeApple(), API: billing.NewFakeAPI(), PKI: hostedtest.NewPKI(now.AddDate(-1, 0, 0)),
		Clock: clock, Bundle: bundle, Product: product, Env: billing.EnvSandbox}
	s.verifier = &billing.Verifier{Roots: s.PKI.Pool, Clock: clock, LeafOID: hostedtest.LeafOID, IntermediateOID: hostedtest.IntermediateOID}
	return s
}

// Verifier verifies what this fake signs (use it instead of the pinned Apple verifier).
func (s *Server) Verifier() *billing.Verifier { return s.verifier }

// IdentityToken signs a Sign in with Apple identity token for sub bound to the raw nonce.
func (s *Server) IdentityToken(sub, nonce string) string {
	now := s.Clock.Now()
	return s.IDP.Sign("rsa-1", map[string]any{"iss": appleauth.Issuer, "aud": s.Bundle, "sub": sub, "nonce": appleauth.NonceClaim(nonce),
		"exp": now.Add(10 * time.Minute).Unix(), "iat": now.Unix(), "email": sub + "@privaterelay.appleid.com", "is_private_email": "true"})
}

// Transaction builds a transaction for an account token. days is the length of the period.
func (s *Server) Transaction(appAccountToken, otx string, trial bool, days int) billing.TransactionInfo {
	now := s.Clock.Now()
	if days <= 0 {
		days = 14
		if !trial {
			days = 30
		}
	}
	t := billing.TransactionInfo{TransactionID: otx + "-" + now.Format("20060102150405"), OriginalTransactionID: otx, BundleID: s.Bundle, ProductID: s.Product,
		OriginalPurchaseDate: billing.MsOf(now), PurchaseDate: billing.MsOf(now), ExpiresDate: billing.MsOf(now.AddDate(0, 0, days)), Type: "Auto-Renewable Subscription",
		AppAccountToken: appAccountToken, Environment: s.Env, SignedDate: billing.MsOf(now)}
	if trial {
		t.OfferType, t.OfferDiscountType = 1, billing.DiscountFreeTrial
	}
	return t
}

// SetStatus tells the fake App Store Server API what Apple says (1 active ... 5 revoked).
func (s *Server) SetStatus(tx billing.TransactionInfo, status int, autoRenew bool) {
	ar := 0
	if autoRenew {
		ar = 1
	}
	s.API.SetStatus(tx.OriginalTransactionID, billing.SubscriptionStatus{Status: status, Transaction: tx,
		Renewal: billing.RenewalInfo{AutoRenewStatus: ar, AutoRenewProductID: s.Product}, Environment: s.Env})
}

// Notification signs a notification (App Store Server Notifications V2).
func (s *Server) Notification(typ, subtype, uuid string, tx billing.TransactionInfo, autoRenew bool) string {
	now := s.Clock.Now()
	ar := 0
	if autoRenew {
		ar = 1
	}
	ren := billing.RenewalInfo{AutoRenewStatus: ar, AutoRenewProductID: s.Product}
	data := billing.NotificationData{BundleID: s.Bundle, Environment: s.Env, SignedTransactionInfo: s.PKI.Sign(tx), SignedRenewalInfo: s.PKI.Sign(ren)}
	return s.PKI.Sign(billing.NotificationPayload{NotificationType: typ, Subtype: subtype, NotificationUUID: uuid, Version: "2.0", SignedDate: billing.MsOf(now), Data: data})
}

// Handle serves the development routes: what is "identity-token", "transaction", "notification" or
// "siwa-event". Bodies and answers are JSON.
func (s *Server) Handle(w http.ResponseWriter, r *http.Request, what string) {
	var in struct {
		Sub                   string `json:"sub"`
		Nonce                 string `json:"nonce"`
		AppAccountToken       string `json:"app_account_token"`
		OriginalTransactionID string `json:"original_transaction_id"`
		Trial                 *bool  `json:"trial"`
		Days                  int    `json:"days"`
		Status                int    `json:"status"`
		AutoRenew             *bool  `json:"auto_renew"`
		Type                  string `json:"type"`
		Subtype               string `json:"subtype"`
		UUID                  string `json:"uuid"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in)
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(msg string) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Invalid request", "status": 400, "code": "invalid_request", "detail": msg})
	}
	trial := in.Trial == nil || *in.Trial
	autoRenew := in.AutoRenew == nil || *in.AutoRenew
	switch what {
	case "identity-token":
		if in.Sub == "" || in.Nonce == "" {
			fail("sub and nonce are required")
			return
		}
		code := "fake-code-" + in.Sub + "-" + in.Nonce[:min(6, len(in.Nonce))]
		s.Apple.AddCode(code, "fake-refresh-"+in.Sub)
		reply(map[string]any{"identity_token": s.IdentityToken(in.Sub, in.Nonce), "authorization_code": code})
	case "transaction", "notification":
		if in.OriginalTransactionID == "" {
			fail("original_transaction_id is required")
			return
		}
		tx := s.Transaction(in.AppAccountToken, in.OriginalTransactionID, trial, in.Days)
		status := in.Status
		if status == 0 {
			status = 1
		}
		if what == "transaction" {
			s.SetStatus(tx, status, autoRenew)
			reply(map[string]any{"signed_transaction": s.PKI.Sign(tx)})
			return
		}
		if in.Type == "" {
			fail("type is required")
			return
		}
		switch in.Type {
		case billing.NtExpired, billing.NtRefund:
			status = 2
			tx.ExpiresDate = billing.MsOf(s.Clock.Now())
		case billing.NtDidFailToRenew:
			status = 3
			if in.Subtype == billing.SubGracePeriod {
				status = 4
			}
		}
		if in.Type == billing.NtRefund {
			tx.RevocationDate = billing.MsOf(s.Clock.Now())
		}
		if in.Status == 0 || in.Type != "" {
			s.SetStatus(tx, status, autoRenew)
		}
		uuid := in.UUID
		if uuid == "" {
			uuid = "fake-" + in.Type + "-" + s.Clock.Now().Format("20060102150405.000000000")
		}
		reply(map[string]any{"signedPayload": s.Notification(in.Type, in.Subtype, uuid, tx, autoRenew)})
	case "siwa-event":
		if in.Sub == "" || in.Type == "" {
			fail("sub and type are required")
			return
		}
		ev, _ := json.Marshal(map[string]any{"type": in.Type, "sub": in.Sub, "event_time": s.Clock.Now().UnixMilli()})
		reply(map[string]any{"payload": s.IDP.Sign("rsa-1", map[string]any{"iss": appleauth.Issuer, "aud": s.Bundle, "iat": s.Clock.Now().Unix(),
			"jti": "fake-" + s.Clock.Now().Format("20060102150405.000000000"), "events": string(ev)})})
	default:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Not found", "status": 404, "code": "not_found"})
	}
}

// ErrProduction is returned when someone tries to use the fake with the production environment.
var ErrProduction = errors.New("fakeapple: refused in the production environment")
