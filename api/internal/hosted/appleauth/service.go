package appleauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"shop/internal/hosted/control"
)

// CodeExchanger exchanges an authorization code for a refresh token at Apple's token endpoint
// (signed with the Sign in with Apple client secret). The real client is a later step.
type CodeExchanger interface {
	Exchange(ctx context.Context, authorizationCode string) (refreshToken string, err error)
}

// TokenRevoker revokes a refresh token at Apple (POST appleid.apple.com/auth/revoke).
type TokenRevoker interface {
	Revoke(ctx context.Context, refreshToken string) error
}

// Errors an exchanger or revoker may return.
var (
	ErrCodeInvalid      = errors.New("authorization code rejected")
	ErrAppleUnavailable = errors.New("apple token endpoint unavailable")
)

// Sealer encrypts the stored refresh token (key from the server environment, never the database).
type Sealer interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// AESGCMSealer is the production Sealer (AES-256-GCM, random nonce prefixed).
type AESGCMSealer struct{ aead cipher.AEAD }

// NewAESGCMSealer needs a 32-byte key.
func NewAESGCMSealer(key []byte) (*AESGCMSealer, error) {
	if len(key) != 32 {
		return nil, errors.New("sealer key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &AESGCMSealer{aead: g}, nil
}

func (s *AESGCMSealer) Seal(plain []byte) ([]byte, error) {
	n := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return s.aead.Seal(n, n, plain, nil), nil
}

func (s *AESGCMSealer) Open(sealed []byte) ([]byte, error) {
	if len(sealed) < s.aead.NonceSize() {
		return nil, errors.New("sealed value too short")
	}
	n, ct := sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():]
	return s.aead.Open(nil, n, ct, nil)
}

// Service is Sign in with Apple on top of the control database.
type Service struct {
	Ctl       *control.Service
	V         *Verifier
	Exchanger CodeExchanger
	Revoker   TokenRevoker
	Sealer    Sealer
	NonceTTL  time.Duration // default 10 minutes
	Log       *slog.Logger
}

// New wires the service.
func New(ctl *control.Service, v *Verifier, ex CodeExchanger, rv TokenRevoker, sealer Sealer) *Service {
	return &Service{Ctl: ctl, V: v, Exchanger: ex, Revoker: rv, Sealer: sealer, NonceTTL: 10 * time.Minute, Log: slog.Default()}
}

// IssueNonce creates a single-use nonce: 32 random bytes, base64url without padding (43
// characters), stored only as a hash, valid for NonceTTL.
func (s *Service) IssueNonce(ctx context.Context) (string, time.Time, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, err
	}
	nonce := base64.RawURLEncoding.EncodeToString(b[:])
	exp := s.Ctl.S.Now().Add(s.NonceTTL)
	if err := s.Ctl.S.InsertNonce(ctx, control.HashSecret(nonce), exp); err != nil {
		return "", time.Time{}, err
	}
	return nonce, exp, nil
}

// SignInRequest is POST /v1/auth/apple.
type SignInRequest struct {
	IdentityToken     string
	AuthorizationCode string
	Nonce             string
	DeviceName        string
	GivenName         string
	FamilyName        string
}

// SignInResult is the outcome: Created is true when the account was just created (HTTP 201).
type SignInResult struct {
	Created bool
	Token   string
	Device  control.Device
	Account control.Account
}

func invalidRequest(msg string) error {
	return &control.Error{Code: "invalid_request", Status: 400, Msg: msg}
}

// DisplayName builds "Ama B." from the first-authorization name (max 60 characters).
func DisplayName(given, family string) string {
	given, family = strings.TrimSpace(given), strings.TrimSpace(family)
	name := given
	if family != "" {
		r, _ := utf8.DecodeRuneInString(family)
		if name != "" {
			name += " "
		}
		name += string(r) + "."
	}
	if utf8.RuneCountInString(name) > 60 {
		name = string([]rune(name)[:60])
	}
	return name
}

// SignIn runs the whole flow: the nonce is consumed first (so a replay burns it even when
// the token turns out bad), then the identity token is verified, the authorization code is
// exchanged, the account is created or found, and a device token is minted.
func (s *Service) SignIn(ctx context.Context, req SignInRequest) (SignInResult, error) {
	var out SignInResult
	req.DeviceName = strings.TrimSpace(req.DeviceName)
	if req.IdentityToken == "" || req.AuthorizationCode == "" || req.Nonce == "" || req.DeviceName == "" || utf8.RuneCountInString(req.DeviceName) > 60 {
		return out, invalidRequest("identity_token, authorization_code, nonce and device_name (max 60) are required")
	}
	ok, err := s.Ctl.S.ConsumeNonce(ctx, control.HashSecret(req.Nonce), s.Ctl.S.Now())
	if err != nil {
		return out, err
	}
	if !ok {
		return out, ErrNonceInvalid
	}
	claims, err := s.V.VerifyIdentityToken(ctx, req.IdentityToken)
	if err != nil {
		return out, err
	}
	if claims.Nonce != NonceClaim(req.Nonce) {
		return out, fmt.Errorf("%w: nonce claim does not match", ErrTokenInvalid)
	}
	if existing, err := s.Ctl.S.AccountBySub(ctx, claims.Sub); err == nil {
		if existing.Status == control.AccountLocked || existing.Status == control.AccountDisabled {
			return out, control.ErrAccountLocked
		}
	} else if !errors.Is(err, control.ErrNotFound) {
		return out, err
	}
	refresh, err := s.Exchanger.Exchange(ctx, req.AuthorizationCode)
	switch {
	case errors.Is(err, ErrCodeInvalid):
		return out, fmt.Errorf("%w: authorization code", ErrTokenInvalid)
	case err != nil:
		return out, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	sealed, err := s.Sealer.Seal([]byte(refresh))
	if err != nil {
		return out, err
	}
	err = s.Ctl.S.Tx(ctx, func(tx *control.Store) error {
		svc := s.Ctl.WithStore(tx)
		acc, created, err := svc.UpsertAccount(ctx, claims.Sub, claims.Email, claims.IsPrivateEmail, DisplayName(req.GivenName, req.FamilyName))
		if err != nil {
			return err
		}
		if err := tx.SetRefreshToken(ctx, acc.ID, sealed); err != nil {
			return err
		}
		d, tok, err := svc.CreateDevice(ctx, acc.ID, req.DeviceName)
		if err != nil {
			return err
		}
		acc.RefreshTokenEnc = sealed
		out = SignInResult{Created: created, Token: tok, Device: d, Account: acc}
		return tx.AppendAudit(ctx, acc.ID, "auth.sign_in", acc.ID, map[string]any{"device": d.ID, "created": created})
	})
	return out, err
}

// RevokePending tries to revoke the queued Apple refresh token of one account (the queue is
// filled by control.Service.RequestAccountDeletion). A failure is recorded and retried later;
// it never fails the user's request. It reports whether the token is now revoked.
func (s *Service) RevokePending(ctx context.Context, accountID string) (bool, error) {
	pend, err := s.Ctl.S.PendingRevocations(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range pend {
		if p.AccountID == accountID {
			return s.revokeOne(ctx, p), nil
		}
	}
	return true, nil // nothing pending
}

func (s *Service) revokeOne(ctx context.Context, p control.PendingRevocation) bool {
	plain, err := s.Sealer.Open(p.TokenEnc)
	if err == nil {
		err = s.Revoker.Revoke(ctx, string(plain))
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		s.Log.Warn("appleauth: revoke failed, will retry", "account", p.AccountID, "err", err)
	}
	if ferr := s.Ctl.S.FinishRevocation(ctx, p.AccountID, msg); ferr != nil {
		s.Log.Error("appleauth: cannot record revocation result", "err", ferr)
	}
	return err == nil
}

// RetryRevocations works through every queued revocation (called by the lifecycle job).
func (s *Service) RetryRevocations(ctx context.Context) (done, failed int, err error) {
	pend, err := s.Ctl.S.PendingRevocations(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range pend {
		if s.revokeOne(ctx, p) {
			done++
		} else {
			failed++
		}
	}
	return done, failed, nil
}

// ---- server-to-server events ----

// Event types Apple sends.
const (
	EvEmailDisabled  = "email-disabled"
	EvEmailEnabled   = "email-enabled"
	EvConsentRevoked = "consent-revoked"
	EvAccountDeleted = "account-deleted"
)

// EventResult says what HandleEvent did.
type EventResult struct {
	Type      string
	AccountID string
	Outcome   string // applied, duplicate, unknown_account, ignored
}

// HandleEvent verifies and applies a Sign in with Apple server-to-server event (the payload of
// POST /v1/auth/apple/events). Idempotent by the token's jti. An invalid token wraps
// ErrTokenInvalid (HTTP 400); other errors are transient (HTTP 500, Apple retries).
//
//	email-disabled / email-enabled: update email_deliverable
//	consent-revoked: revoke every device of the account, keep the data
//	account-deleted: lock the account, revoke devices, start the lapse path for the owned
//	                 household (30 days read-only, never an instant wipe)
func (s *Service) HandleEvent(ctx context.Context, payload string) (EventResult, error) {
	var res EventResult
	claims, err := s.V.VerifyEventToken(ctx, payload)
	if err != nil {
		return res, err
	}
	var ev struct {
		Type string `json:"type"`
		Sub  string `json:"sub"`
	}
	if claims.Events == "" || json.Unmarshal([]byte(claims.Events), &ev) != nil || ev.Type == "" || ev.Sub == "" {
		return res, fmt.Errorf("%w: events claim", ErrTokenInvalid)
	}
	res.Type = ev.Type
	id := claims.Jti
	if id == "" {
		id = "sig:" + control.HashSecret(payload)
	}
	err = s.Ctl.S.Tx(ctx, func(tx *control.Store) error {
		inserted, err := tx.InsertNotification(ctx, control.NotificationRecord{UUID: "siwa:" + id, Type: "siwa:" + ev.Type})
		if err != nil {
			return err
		}
		if !inserted {
			res.Outcome = "duplicate"
			return nil
		}
		acc, err := tx.AccountBySub(ctx, ev.Sub)
		if errors.Is(err, control.ErrNotFound) {
			res.Outcome = "unknown_account"
			return nil
		}
		if err != nil {
			return err
		}
		res.AccountID = acc.ID
		now := tx.Now()
		switch ev.Type {
		case EvEmailDisabled:
			err = tx.SetEmailDeliverable(ctx, acc.ID, false)
		case EvEmailEnabled:
			err = tx.SetEmailDeliverable(ctx, acc.ID, true)
		case EvConsentRevoked:
			_, err = tx.RevokeAccountDevices(ctx, acc.ID, 0, now)
		case EvAccountDeleted:
			if err = tx.SetAccountStatus(ctx, acc.ID, control.AccountLocked); err != nil {
				return err
			}
			if _, err = tx.RevokeAccountDevices(ctx, acc.ID, 0, now); err != nil {
				return err
			}
			if h, m, herr := tx.HouseholdOfAccount(ctx, acc.ID); herr == nil && m.Role == control.RoleOwner && h.ForcedLapseAt.IsZero() {
				err = tx.SetHouseholdForcedLapse(ctx, h.ID, now)
			}
		default:
			res.Outcome = "ignored"
			return nil
		}
		if err != nil {
			return err
		}
		res.Outcome = "applied"
		return tx.AppendAudit(ctx, "apple", "siwa."+ev.Type, acc.ID, nil)
	})
	return res, err
}
