package control

import (
	"context"
	"errors"
	"time"
)

// DeleteConfirmText is the literal the client must send to start account deletion.
const DeleteConfirmText = "delete my account"

// Config holds the household invariants. Defaults follow the contract.
type Config struct {
	MemberCap          int           // members including the owner (4)
	MaxOpenInvites     int           // open invites per household (5)
	InviteTTL          time.Duration // 7 days
	DeletionCoolingOff time.Duration // 7 days between request and removal
}

// DefaultConfig returns the launch values.
func DefaultConfig() Config {
	return Config{MemberCap: 4, MaxOpenInvites: 5, InviteTTL: 7 * 24 * time.Hour, DeletionCoolingOff: 7 * 24 * time.Hour}
}

// AccessChecker says whether an account currently holds an entitlement that gives full
// access (the plan calculator in billing provides it). Used by the transfer rule.
type AccessChecker func(ctx context.Context, accountID string, now time.Time) (bool, error)

// Service holds the invariants on top of Store.
type Service struct {
	S       *Store
	Cfg     Config
	HasPlan AccessChecker
}

// NewService builds a Service. hasPlan may be nil (then no account counts as having a plan).
func NewService(s *Store, cfg Config, hasPlan AccessChecker) *Service {
	if cfg.MemberCap == 0 {
		cfg = DefaultConfig()
	}
	return &Service{S: s, Cfg: cfg, HasPlan: hasPlan}
}

// UpsertAccount returns the account for an Apple user id, creating it on first sight.
// name is stored only when the account has no display name yet.
func (v *Service) UpsertAccount(ctx context.Context, sub, email string, emailIsPrivate bool, displayName string) (Account, bool, error) {
	var out Account
	created := false
	err := v.S.Tx(ctx, func(tx *Store) error {
		a, err := tx.AccountBySub(ctx, sub)
		switch {
		case err == nil:
			if a.DisplayName == "" && displayName != "" {
				if err := tx.SetAccountDisplayName(ctx, a.ID, displayName); err != nil {
					return err
				}
				a.DisplayName = displayName
			}
			if email != "" && a.Email != email {
				if err := tx.SetAccountEmail(ctx, a.ID, email, emailIsPrivate); err != nil {
					return err
				}
				a.Email, a.EmailIsPrivate = email, emailIsPrivate
			}
			now := tx.Now()
			if err := tx.SetAccountLogin(ctx, a.ID, now); err != nil {
				return err
			}
			a.LastLoginAt = now
			out = a
			return nil
		case errors.Is(err, ErrNotFound):
		default:
			return err
		}
		now := tx.Now()
		a, err = tx.InsertAccount(ctx, Account{AppleSub: sub, DisplayName: displayName, Email: email, EmailIsPrivate: emailIsPrivate, EmailDeliverable: true, LastLoginAt: now})
		if err != nil {
			return err
		}
		created = true
		out = a
		return tx.AppendAudit(ctx, "system", "account.create", a.ID, nil)
	})
	return out, created, err
}

// CreateDevice mints a device token for the account. The token is returned once.
func (v *Service) CreateDevice(ctx context.Context, accountID, name string) (Device, string, error) {
	token, hash := NewToken()
	d, err := v.S.InsertDevice(ctx, Device{AccountID: accountID, TokenHash: hash, Name: name})
	return d, token, err
}

// Principal is who a bearer token belongs to.
type Principal struct {
	Account Account
	Device  Device
}

// Authenticate resolves a bearer token. Unknown or revoked: ErrNotFound (HTTP 401
// token_invalid). Locked account: ErrAccountLocked.
func (v *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	d, err := v.S.DeviceByTokenHash(ctx, HashSecret(token))
	if err != nil || !d.RevokedAt.IsZero() {
		return Principal{}, ErrNotFound
	}
	a, err := v.S.AccountByID(ctx, d.AccountID)
	if err != nil {
		return Principal{}, err
	}
	if a.Status != AccountActive {
		if a.Status == AccountLocked || a.Status == AccountDisabled {
			return Principal{Account: a, Device: d}, ErrAccountLocked
		}
		return Principal{}, ErrNotFound
	}
	_ = v.S.TouchDevice(ctx, d.ID, v.S.Now())
	return Principal{Account: a, Device: d}, nil
}

// EnsureHousehold returns the account's household, creating it (with the account as owner)
// when it has none. Called when the first entitlement (trial, purchase, grant) appears.
func (v *Service) EnsureHousehold(ctx context.Context, ownerID string) (Household, bool, error) {
	var out Household
	created := false
	err := v.S.Tx(ctx, func(tx *Store) error {
		if h, _, err := tx.HouseholdOfAccount(ctx, ownerID); err == nil {
			out = h
			return nil
		} else if !errors.Is(err, ErrNoHousehold) {
			return err
		}
		a, err := tx.AccountByID(ctx, ownerID)
		if err != nil {
			return err
		}
		if a.Status != AccountActive {
			return ErrAccountLocked
		}
		h, err := tx.InsertHousehold(ctx, Household{OwnerAccountID: ownerID})
		if err != nil {
			return err
		}
		if err := tx.AddMember(ctx, Member{HouseholdID: h.ID, AccountID: ownerID, Role: RoleOwner}); err != nil {
			return err
		}
		created = true
		out = h
		return tx.AppendAudit(ctx, "system", "household.create", h.ID, map[string]any{"owner": ownerID})
	})
	return out, created, err
}

// ownerContext loads the caller's membership and checks that it is the owner of an active household.
func (v *Service) ownerContext(ctx context.Context, tx *Store, accountID string) (Household, error) {
	h, m, err := tx.HouseholdOfAccount(ctx, accountID)
	if err != nil {
		return Household{}, err
	}
	if m.Role != RoleOwner {
		return Household{}, ErrNotOwner
	}
	return h, nil
}

// CreateInvite makes a single-use invite. The plaintext code (grouped, 12 characters) is
// returned once; only its hash is stored. Members plus open invites count against the cap.
func (v *Service) CreateInvite(ctx context.Context, ownerID string) (Invite, string, error) {
	var inv Invite
	var code string
	err := v.S.Tx(ctx, func(tx *Store) error {
		h, err := v.ownerContext(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		now := tx.Now()
		members, err := tx.CountMembers(ctx, h.ID)
		if err != nil {
			return err
		}
		open, err := tx.OpenInvites(ctx, h.ID, now)
		if err != nil {
			return err
		}
		if members+len(open) >= v.Cfg.MemberCap {
			return ErrHouseholdFull
		}
		if len(open) >= v.Cfg.MaxOpenInvites {
			return &LimitError{Limit: "open_invites", Value: int64(v.Cfg.MaxOpenInvites), Current: int64(len(open))}
		}
		raw := RandomCode(12)
		code = GroupCode(raw)
		inv = Invite{ID: NewUUID(), HouseholdID: h.ID, CodeHash: HashCode(raw), CreatedBy: ownerID, CreatedAt: now, ExpiresAt: now.Add(v.Cfg.InviteTTL)}
		if err := tx.InsertInvite(ctx, inv); err != nil {
			return err
		}
		return tx.AppendAudit(ctx, ownerID, "invite.create", h.ID, map[string]any{"invite": inv.ID})
	})
	return inv, code, err
}

// RevokeInvite revokes an open invite of the owner's household.
func (v *Service) RevokeInvite(ctx context.Context, ownerID, inviteID string) error {
	return v.S.Tx(ctx, func(tx *Store) error {
		h, err := v.ownerContext(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		ok, err := tx.RevokeInvite(ctx, inviteID, h.ID, tx.Now())
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		return tx.AppendAudit(ctx, ownerID, "invite.revoke", h.ID, map[string]any{"invite": inviteID})
	})
}

// AcceptInvite adds the account to the invite's household. Check order follows the contract:
// invite validity, then already_in_household, then household_full. The invite is consumed
// only when the join succeeds.
func (v *Service) AcceptInvite(ctx context.Context, accountID, code string) (Household, error) {
	var out Household
	err := v.S.Tx(ctx, func(tx *Store) error {
		a, err := tx.AccountByID(ctx, accountID)
		if err != nil {
			return err
		}
		if a.Status != AccountActive {
			return ErrAccountLocked
		}
		now := tx.Now()
		inv, err := tx.InviteByHash(ctx, HashCode(code))
		if err != nil || !inv.Open(now) {
			return ErrInviteInvalid
		}
		h, err := tx.HouseholdByID(ctx, inv.HouseholdID)
		if err != nil || h.Status != HouseholdActive {
			return ErrInviteInvalid
		}
		if _, err := tx.MemberOf(ctx, accountID); err == nil {
			return ErrAlreadyInHousehold
		} else if !errors.Is(err, ErrNoHousehold) {
			return err
		}
		n, err := tx.CountMembers(ctx, h.ID)
		if err != nil {
			return err
		}
		if n >= v.Cfg.MemberCap {
			return ErrHouseholdFull
		}
		ok, err := tx.MarkInviteUsed(ctx, inv.ID, accountID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInviteInvalid
		}
		if err := tx.AddMember(ctx, Member{HouseholdID: h.ID, AccountID: accountID, Role: RoleMember, JoinedAt: now}); err != nil {
			return err
		}
		out = h
		return tx.AppendAudit(ctx, accountID, "invite.accept", h.ID, map[string]any{"invite": inv.ID})
	})
	return out, err
}

// RemoveMember lets the owner remove a member. Their devices stay valid for the account.
func (v *Service) RemoveMember(ctx context.Context, ownerID, targetID string) error {
	return v.S.Tx(ctx, func(tx *Store) error {
		h, err := v.ownerContext(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		if targetID == ownerID {
			return ErrCannotRemoveOwner
		}
		m, err := tx.MemberOf(ctx, targetID)
		if err != nil || m.HouseholdID != h.ID {
			return ErrNotFound
		}
		if err := tx.RemoveMember(ctx, h.ID, targetID); err != nil {
			return err
		}
		return tx.AppendAudit(ctx, ownerID, "member.remove", h.ID, map[string]any{"account": targetID})
	})
}

// Leave removes a member from their household. The owner must transfer or delete.
func (v *Service) Leave(ctx context.Context, accountID string) error {
	return v.S.Tx(ctx, func(tx *Store) error {
		m, err := tx.MemberOf(ctx, accountID)
		if err != nil {
			return err
		}
		if m.Role == RoleOwner {
			return ErrOwnerCannotLeave
		}
		if err := tx.RemoveMember(ctx, m.HouseholdID, accountID); err != nil {
			return err
		}
		return tx.AppendAudit(ctx, accountID, "member.leave", m.HouseholdID, nil)
	})
}

// Transfer hands ownership to another member. The household's plan follows the new owner's
// entitlements; if the target has no plan the caller must confirm (the household then lapses).
func (v *Service) Transfer(ctx context.Context, ownerID, targetID string, confirmPlanChange bool) (Household, error) {
	var out Household
	err := v.S.Tx(ctx, func(tx *Store) error {
		h, err := v.ownerContext(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		if targetID == ownerID {
			return ErrInvalidRequest
		}
		m, err := tx.MemberOf(ctx, targetID)
		if err != nil || m.HouseholdID != h.ID {
			return ErrNotFound
		}
		has := false
		if v.HasPlan != nil {
			has, err = v.HasPlan(ctx, targetID, tx.Now())
			if err != nil {
				return err
			}
		}
		if !has && !confirmPlanChange {
			return ErrTransferTargetNoPlan
		}
		if err := tx.SetMemberRole(ctx, h.ID, ownerID, RoleMember); err != nil {
			return err
		}
		if err := tx.SetMemberRole(ctx, h.ID, targetID, RoleOwner); err != nil {
			return err
		}
		if err := tx.SetHouseholdOwner(ctx, h.ID, targetID); err != nil {
			return err
		}
		if !has { // the household lapses now (30 day clock) unless the new owner subscribes
			if err := tx.SetHouseholdForcedLapse(ctx, h.ID, tx.Now()); err != nil {
				return err
			}
		}
		h.OwnerAccountID = targetID
		out = h
		return tx.AppendAudit(ctx, ownerID, "household.transfer", h.ID, map[string]any{"to": targetID, "target_has_plan": has})
	})
	return out, err
}

// HouseholdView is the data behind GET /v1/household.
type HouseholdView struct {
	Household      Household
	Members        []Member
	PendingInvites []Invite // owner only
	YourRole       string
}

// ViewHousehold loads the household of an account.
func (v *Service) ViewHousehold(ctx context.Context, accountID string) (HouseholdView, error) {
	h, m, err := v.S.HouseholdOfAccount(ctx, accountID)
	if err != nil {
		return HouseholdView{}, err
	}
	members, err := v.S.Members(ctx, h.ID)
	if err != nil {
		return HouseholdView{}, err
	}
	out := HouseholdView{Household: h, Members: members, YourRole: m.Role}
	if m.Role == RoleOwner {
		out.PendingInvites, err = v.S.OpenInvites(ctx, h.ID, v.S.Now())
	}
	return out, err
}

// DeletionStatus is the answer to DELETE /v1/account.
type DeletionStatus struct {
	RequestedAt time.Time
	DeleteAfter time.Time
}

// RequestAccountDeletion is stage 1: flags the account (and the household of an owner), revokes
// every device except keepDeviceID, queues the Apple token revocation (the encrypted refresh
// token moves to the pending table; a failure there never fails the request) and detaches a
// member. Stage 2 is the lifecycle job after DeletionCoolingOff. Repeated calls are idempotent.
func (v *Service) RequestAccountDeletion(ctx context.Context, accountID, confirm string, deleteHousehold bool, keepDeviceID int64) (DeletionStatus, error) {
	if confirm != DeleteConfirmText {
		return DeletionStatus{}, ErrInvalidRequest
	}
	var st DeletionStatus
	err := v.S.Tx(ctx, func(tx *Store) error {
		a, err := tx.AccountByID(ctx, accountID)
		if err != nil {
			return err
		}
		if !a.DeletionRequestedAt.IsZero() {
			st = DeletionStatus{RequestedAt: a.DeletionRequestedAt, DeleteAfter: a.DeletionRequestedAt.Add(v.Cfg.DeletionCoolingOff)}
			return nil
		}
		m, merr := tx.MemberOf(ctx, accountID)
		hasHH := merr == nil
		if merr != nil && !errors.Is(merr, ErrNoHousehold) {
			return merr
		}
		if hasHH && m.Role == RoleOwner && !deleteHousehold {
			return ErrOwnerMustConfirm
		}
		if hasHH && m.Role == RoleMember && deleteHousehold {
			return ErrNotHouseholdOwnerDelete
		}
		now := tx.Now()
		if err := tx.SetAccountDeletionRequested(ctx, accountID, now); err != nil {
			return err
		}
		if _, err := tx.RevokeAccountDevices(ctx, accountID, keepDeviceID, now); err != nil {
			return err
		}
		if len(a.RefreshTokenEnc) > 0 {
			if err := tx.AddPendingRevocation(ctx, accountID, a.RefreshTokenEnc); err != nil {
				return err
			}
			if err := tx.SetRefreshToken(ctx, accountID, nil); err != nil {
				return err
			}
		}
		if hasHH {
			if m.Role == RoleOwner {
				if err := tx.SetHouseholdDeletionRequested(ctx, m.HouseholdID, now); err != nil {
					return err
				}
				if err := tx.RevokeHouseholdInvites(ctx, m.HouseholdID, now); err != nil {
					return err
				}
			} else if err := tx.RemoveMember(ctx, m.HouseholdID, accountID); err != nil {
				return err
			}
		}
		st = DeletionStatus{RequestedAt: now, DeleteAfter: now.Add(v.Cfg.DeletionCoolingOff)}
		return tx.AppendAudit(ctx, accountID, "account.deletion_requested", accountID, map[string]any{"delete_household": deleteHousehold})
	})
	return st, err
}

// CancelAccountDeletion clears a pending request within the cooling-off window.
func (v *Service) CancelAccountDeletion(ctx context.Context, accountID string) error {
	return v.S.Tx(ctx, func(tx *Store) error {
		a, err := tx.AccountByID(ctx, accountID)
		if err != nil {
			return err
		}
		if a.DeletionRequestedAt.IsZero() || a.Status == AccountDeleted {
			return ErrDeletionNotPending
		}
		hs, err := tx.LatestOwnedHousehold(ctx, accountID)
		if err == nil && !hs.DeletionRequestedAt.IsZero() {
			if hs.Status == HouseholdDeleted {
				return ErrDeletionNotPending
			}
			if hs.Status == HouseholdTombstoned {
				if err := tx.UntombstoneHousehold(ctx, hs.ID); err != nil {
					return err
				}
			}
			if err := tx.SetHouseholdDeletionRequested(ctx, hs.ID, time.Time{}); err != nil {
				return err
			}
		}
		if err := tx.SetAccountDeletionRequested(ctx, accountID, time.Time{}); err != nil {
			return err
		}
		return tx.AppendAudit(ctx, accountID, "account.deletion_cancelled", accountID, nil)
	})
}

// WithStore returns a copy of the service bound to another Store, typically the transaction
// handed to Store.Tx. Use it to call service methods from inside a transaction.
func (v *Service) WithStore(s *Store) *Service {
	c := *v
	c.S = s
	return &c
}
