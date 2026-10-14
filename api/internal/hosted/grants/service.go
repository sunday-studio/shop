package grants

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"shop/internal/hosted/control"
)

// Errors (contract section 6). Compare with control.Is.
var (
	ErrCapReached = &control.Error{Code: "grant_cap_reached", Status: http.StatusConflict, Msg: "the maximum number of active grants is reached"}
	ErrExists     = &control.Error{Code: "grant_exists", Status: http.StatusConflict, Msg: "an active grant exists for this account; extend it"}
)

func invalid(msg string) error {
	return &control.Error{Code: "invalid_request", Status: http.StatusBadRequest, Msg: msg}
}

// Config holds the grant policy. Defaults follow the owner decisions: 90 days, max 366, cap 50.
type Config struct {
	DefaultDays int
	MaxDays     int
	MaxActive   int
}

// DefaultConfig returns the launch policy.
func DefaultConfig() Config { return Config{DefaultDays: 90, MaxDays: 366, MaxActive: 50} }

// Status of a grant at a point in time.
type Status string

const (
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
)

// StatusAt computes a grant's status.
func StatusAt(g control.Grant, now time.Time) Status {
	switch {
	case !g.RevokedAt.IsZero():
		return StatusRevoked
	case !now.Before(g.ExpiresAt):
		return StatusExpired
	default:
		return StatusActive
	}
}

// Service creates, extends, revokes and lists grants. Every change writes an audit row and
// keeps the grant's entitlement row (source "grant") in step, in one transaction.
type Service struct {
	Ctl *control.Service
	Cfg Config
}

// New builds a Service.
func New(ctl *control.Service, cfg Config) *Service {
	if cfg.DefaultDays == 0 {
		cfg = DefaultConfig()
	}
	return &Service{Ctl: ctl, Cfg: cfg}
}

// CreateInput is one grant request. Give AccountID or SupportCode.
type CreateInput struct {
	AccountID      string
	SupportCode    string
	Key            string
	ExpiresAt      time.Time // optional
	Days           int       // optional; used when ExpiresAt is zero
	Reason         string
	Actor          string // operator name (required)
	IdempotencyKey string
}

// Create makes a grant. When IdempotencyKey was seen before the first grant is returned and
// replay is true. The grantee's household is created immediately if they have none.
func (s *Service) Create(ctx context.Context, in CreateInput) (g control.Grant, replay bool, err error) {
	st := s.Ctl.S
	in.Reason, in.Actor = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Actor)
	if in.Reason == "" {
		return g, false, invalid("reason is required")
	}
	if in.Actor == "" || len(in.Actor) > 40 {
		return g, false, invalid("operator name is required (1 to 40 characters)")
	}
	if in.Key == "" {
		in.Key = control.KeyHosted
	}
	if in.Key != control.KeyHosted {
		return g, false, invalid("unknown entitlement key")
	}
	err = st.Tx(ctx, func(tx *control.Store) error {
		if in.IdempotencyKey != "" {
			if prev, err := tx.GrantByIdempotencyKey(ctx, in.IdempotencyKey); err == nil {
				g, replay = prev, true
				return nil
			} else if !errors.Is(err, control.ErrNotFound) {
				return err
			}
		}
		now := tx.Now()
		var acc control.Account
		var err error
		switch {
		case in.AccountID != "":
			acc, err = tx.AccountByID(ctx, in.AccountID)
		case in.SupportCode != "":
			acc, err = tx.AccountBySupportCode(ctx, in.SupportCode)
		default:
			return invalid("account_id or support_code is required")
		}
		if err != nil {
			return err
		}
		if acc.Status != control.AccountActive {
			return control.ErrAccountLocked
		}
		expires := in.ExpiresAt
		if expires.IsZero() {
			days := in.Days
			if days == 0 {
				days = s.Cfg.DefaultDays
			}
			if days < 0 {
				return invalid("days must be positive")
			}
			expires = now.Add(time.Duration(days) * 24 * time.Hour)
		}
		expires = expires.UTC().Truncate(time.Second)
		if !expires.After(now) {
			return invalid("expires_at must be in the future")
		}
		if expires.After(now.Add(time.Duration(s.Cfg.MaxDays) * 24 * time.Hour)) {
			return invalid("a grant cannot last longer than the maximum")
		}
		existing, err := tx.ListGrants(ctx, acc.ID)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.Key == in.Key && StatusAt(e, now) == StatusActive {
				return ErrExists
			}
		}
		n, err := tx.CountActiveGrants(ctx, now)
		if err != nil {
			return err
		}
		if n >= s.Cfg.MaxActive {
			return ErrCapReached
		}
		g, err = tx.InsertGrant(ctx, control.Grant{AccountID: acc.ID, Key: in.Key, ExpiresAt: expires, Reason: in.Reason, CreatedBy: in.Actor, IdempotencyKey: in.IdempotencyKey})
		if err != nil {
			return err
		}
		if _, err := tx.UpsertEntitlement(ctx, control.Entitlement{
			AccountID: acc.ID, Key: in.Key, Source: control.SourceGrant, State: control.EntActive,
			StartsAt: now, ExpiresAt: expires, GrantID: g.ID,
		}); err != nil {
			return err
		}
		if _, _, err := s.Ctl.WithStore(tx).EnsureHousehold(ctx, acc.ID); err != nil {
			return err
		}
		return tx.AppendAudit(ctx, in.Actor, "grant.create", acc.ID, map[string]any{"grant": g.ID, "expires_at": control.FormatTime(expires), "reason": in.Reason})
	})
	return g, replay, err
}

// Extend moves the expiry later (never earlier; revoke instead). A revoked grant cannot be extended.
func (s *Service) Extend(ctx context.Context, id string, expires time.Time, reason, actor string) (control.Grant, error) {
	var out control.Grant
	reason, actor = strings.TrimSpace(reason), strings.TrimSpace(actor)
	if reason == "" || actor == "" {
		return out, invalid("reason and operator are required")
	}
	expires = expires.UTC().Truncate(time.Second)
	err := s.Ctl.S.Tx(ctx, func(tx *control.Store) error {
		g, err := tx.GrantByID(ctx, id)
		if err != nil {
			return err
		}
		now := tx.Now()
		if !g.RevokedAt.IsZero() {
			return invalid("grant is revoked")
		}
		if !expires.After(g.ExpiresAt) {
			return invalid("a grant can only be extended; revoke it to end it sooner")
		}
		if expires.After(now.Add(time.Duration(s.Cfg.MaxDays) * 24 * time.Hour)) {
			return invalid("a grant cannot last longer than the maximum")
		}
		if err := tx.SetGrantExpiry(ctx, id, expires); err != nil {
			return err
		}
		e, err := tx.EntitlementByGrant(ctx, id)
		if err != nil {
			return err
		}
		e.ExpiresAt = expires
		if _, err := tx.UpsertEntitlement(ctx, e); err != nil {
			return err
		}
		g.ExpiresAt = expires
		out = g
		return tx.AppendAudit(ctx, actor, "grant.extend", g.AccountID, map[string]any{"grant": id, "expires_at": control.FormatTime(expires), "reason": reason})
	})
	return out, err
}

// Revoke ends a grant now. Revoking twice is an error (not_found keeps the API small).
func (s *Service) Revoke(ctx context.Context, id, reason, actor string) (control.Grant, error) {
	var out control.Grant
	reason, actor = strings.TrimSpace(reason), strings.TrimSpace(actor)
	if reason == "" || actor == "" {
		return out, invalid("reason and operator are required")
	}
	err := s.Ctl.S.Tx(ctx, func(tx *control.Store) error {
		g, err := tx.GrantByID(ctx, id)
		if err != nil {
			return err
		}
		if !g.RevokedAt.IsZero() {
			return invalid("grant is already revoked")
		}
		now := tx.Now()
		if err := tx.RevokeGrant(ctx, id, actor, reason, now); err != nil {
			return err
		}
		e, err := tx.EntitlementByGrant(ctx, id)
		if err != nil {
			return err
		}
		e.State, e.RevokedAt = control.EntRevoked, now
		if _, err := tx.UpsertEntitlement(ctx, e); err != nil {
			return err
		}
		g.RevokedAt, g.RevokedBy, g.RevokedReason = now, actor, reason
		out = g
		return tx.AppendAudit(ctx, actor, "grant.revoke", g.AccountID, map[string]any{"grant": id, "reason": reason})
	})
	return out, err
}

// ListFilter narrows List. Status "" means all.
type ListFilter struct {
	Status    Status
	AccountID string
}

// List returns grants newest first.
func (s *Service) List(ctx context.Context, f ListFilter) ([]control.Grant, error) {
	all, err := s.Ctl.S.ListGrants(ctx, f.AccountID)
	if err != nil {
		return nil, err
	}
	if f.Status == "" {
		return all, nil
	}
	now := s.Ctl.S.Now()
	var out []control.Grant
	for _, g := range all {
		if StatusAt(g, now) == f.Status {
			out = append(out, g)
		}
	}
	return out, nil
}
