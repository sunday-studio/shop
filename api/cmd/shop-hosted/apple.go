package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/appleclient"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/fakeapple"
	"shop/internal/hosted/hclock"
)

// appleParts are the Apple-facing pieces behind the interfaces the services use.
type appleParts struct {
	Verifier  *billing.Verifier
	Keys      appleauth.KeyFetcher
	Exchanger appleauth.CodeExchanger
	Revoker   appleauth.TokenRevoker
	API       billing.ServerAPI
	// Dev serves the development routes of the fake (nil in live mode).
	Dev func(w http.ResponseWriter, r *http.Request, what string)
	// StoreKit is the real client when configured (admin test notification commands).
	StoreKit *appleclient.StoreKit
}

// unavailableSIWA stands in for the token exchange and revoke clients when no key is configured:
// sign-in answers 502 apple_unavailable instead of crashing the server.
type unavailableSIWA struct{}

func (unavailableSIWA) Exchange(context.Context, string) (string, error) {
	return "", appleauth.ErrAppleUnavailable
}
func (unavailableSIWA) Revoke(context.Context, string) error { return appleauth.ErrAppleUnavailable }

// keyFetcherUnavailable is used when nothing else can be configured.
type keyFetcherUnavailable struct{}

func (keyFetcherUnavailable) FetchJWKS(context.Context) ([]byte, error) {
	return nil, appleauth.ErrKeysUnavailable
}

func buildApple(c hostedConfig, clock hclock.Clock, log *slog.Logger) (*appleParts, error) {
	ap := c.Apple
	if ap.Mode == "fake" {
		if len(ap.ProductIDs) == 0 {
			return nil, errors.New("no product id configured")
		}
		f := fakeapple.New(clock, ap.BundleID, ap.ProductIDs[0])
		log.Warn("SHOP_APPLE_MODE=fake: Apple is simulated in this process. Never use this for real users.")
		return &appleParts{Verifier: f.Verifier(), Keys: f.IDP, Exchanger: f.Apple, Revoker: f.Apple, API: f.API, Dev: f.Handle}, nil
	}
	production := ap.Environment == "production"
	out := &appleParts{}

	// Root of trust for signed transactions and notifications.
	if ap.RootCAFile != "" {
		pemBytes, err := os.ReadFile(ap.RootCAFile)
		if err != nil {
			return nil, fmt.Errorf("SHOP_APPLE_ROOT_CA_FILE: %w", err)
		}
		pool := x509.NewCertPool()
		for {
			var blk *pem.Block
			blk, pemBytes = pem.Decode(pemBytes)
			if blk == nil {
				break
			}
			if cert, err := x509.ParseCertificate(blk.Bytes); err == nil {
				pool.AddCert(cert)
			}
		}
		out.Verifier = &billing.Verifier{Roots: pool, Clock: clock}
		log.Warn("SHOP_APPLE_ROOT_CA_FILE: trusting a test root instead of the pinned Apple root")
	} else {
		out.Verifier = billing.NewPinnedAppleVerifier(clock)
	}

	out.Keys = &appleclient.JWKSFetcher{URL: ap.JWKSURL, Clock: clock}

	siwaKey, kerr := appleclient.LoadKeyFromEnv("SHOP_APPLE_SIWA_KEY")
	if kerr == nil && ap.TeamID != "" && ap.SIWAKeyID != "" {
		cl, err := appleclient.NewSIWAClient(appleclient.SIWAConfig{TeamID: ap.TeamID, KeyID: ap.SIWAKeyID, ClientID: ap.BundleID, Key: siwaKey, BaseURL: ap.SIWAURL, Clock: clock})
		if err != nil {
			return nil, err
		}
		out.Exchanger, out.Revoker = cl, cl
	} else if production {
		return nil, fmt.Errorf("production needs the Sign in with Apple key: SHOP_APPLE_TEAM_ID, SHOP_APPLE_SIWA_KEY_ID and SHOP_APPLE_SIWA_KEY[_FILE] (%v)", kerr)
	} else {
		log.Warn("Sign in with Apple token exchange is not configured: sign-in will answer 502 apple_unavailable")
		out.Exchanger, out.Revoker = unavailableSIWA{}, unavailableSIWA{}
	}

	iapKey, ierr := appleclient.LoadKeyFromEnv("SHOP_APPLE_IAP_KEY")
	if ierr == nil && ap.IAPIssuer != "" && ap.IAPKeyID != "" {
		mode := appleclient.Auto
		switch ap.Environment {
		case "production":
			mode = appleclient.Production
			if ap.AcceptSandbox {
				mode = appleclient.Auto // production first, the sandbox host on "transaction not found" (App Review, TestFlight)
			}
		case "sandbox":
			mode = appleclient.Sandbox
		}
		sk, err := appleclient.NewStoreKit(appleclient.StoreKitConfig{IssuerID: ap.IAPIssuer, KeyID: ap.IAPKeyID, BundleID: ap.BundleID, Key: iapKey, Mode: mode,
			ProductionURL: ap.StoreKitURL, SandboxURL: ap.StoreKitSandboxURL, Decoder: out.Verifier, Clock: clock})
		if err != nil {
			return nil, err
		}
		out.API, out.StoreKit = sk, sk
	} else if production {
		return nil, fmt.Errorf("production needs the in-app purchase key: SHOP_APPLE_IAP_ISSUER_ID, SHOP_APPLE_IAP_KEY_ID and SHOP_APPLE_IAP_KEY[_FILE] (%v)", ierr)
	} else {
		log.Warn("App Store Server API is not configured: purchases will answer 502 apple_unavailable")
		out.API = billing.Unavailable{}
	}
	return out, nil
}
