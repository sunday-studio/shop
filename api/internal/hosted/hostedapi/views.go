package hostedapi

import (
	"context"
	"encoding/json"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/limits"
)

type planOut struct {
	State              string     `json:"state"`
	EntitlementSource  *string    `json:"entitlement_source"`
	Environment        *string    `json:"environment"`
	IsOwner            bool       `json:"is_owner"`
	ReadOnly           bool       `json:"read_only"`
	ProductID          *string    `json:"product_id"`
	TrialEndsAt        *time.Time `json:"trial_ends_at"`
	RenewsAt           *time.Time `json:"renews_at"`
	EndsAt             *time.Time `json:"ends_at"`
	GraceUntil         *time.Time `json:"grace_until"`
	AutoRenew          *bool      `json:"auto_renew"`
	ReadOnlySince      *time.Time `json:"read_only_since"`
	DeleteAfter        *time.Time `json:"delete_after"`
	DeletionRequested  bool       `json:"deletion_requested"`
	ManageSubscription *string    `json:"manage_subscription_url"`
	Allowed            []string   `json:"allowed"`
}

type accountOut struct {
	ID              string    `json:"id"`
	DisplayName     *string   `json:"display_name"`
	Email           *string   `json:"email"`
	EmailIsPrivate  bool      `json:"email_is_private"`
	AppAccountToken string    `json:"app_account_token"`
	SupportCode     string    `json:"support_code"`
	Role            string    `json:"role"`
	HouseholdID     *string   `json:"household_id"`
	CreatedAt       time.Time `json:"created_at"`
	Plan            planOut   `json:"plan"`
}

type usageOut struct {
	limits.Usage
	ReceiptsSoftReached bool `json:"receipts_soft_limit_reached"`
}

type accountFull struct {
	accountOut
	Usage  any `json:"usage"`
	Limits any `json:"limits"`
}

// uploadBlocked reports whether a hard upload limit is reached (monthly, daily, storage, queue).
func (a *API) uploadBlocked(ctx context.Context, hh string, lim limits.Limits, u limits.Usage) bool {
	switch {
	case lim.ReceiptsPerMonth > 0 && u.ReceiptsThisMonth >= lim.ReceiptsPerMonth,
		lim.ReceiptsPerDay > 0 && u.ReceiptsToday >= lim.ReceiptsPerDay,
		lim.StorageBytes > 0 && u.StorageBytes >= lim.StorageBytes,
		lim.MaxQueuedJobs > 0 && u.QueuedJobs >= lim.MaxQueuedJobs:
		return true
	}
	return false
}

func (a *API) planView(p *principal, dropUpload bool) planOut {
	pl := p.Plan
	out := planOut{State: pl.State, EntitlementSource: pl.EntitlementSource, Environment: pl.Environment, IsOwner: pl.IsOwner,
		ReadOnly: pl.ReadOnly, ProductID: pl.ProductID, TrialEndsAt: pl.TrialEndsAt, RenewsAt: pl.RenewsAt, EndsAt: pl.EndsAt,
		GraceUntil: pl.GraceUntil, AutoRenew: pl.AutoRenew, ReadOnlySince: pl.ReadOnlySince, DeleteAfter: pl.DeleteAfter,
		DeletionRequested: pl.DeletionRequested}
	if pl.EntitlementSource != nil && *pl.EntitlementSource == control.SourceApple {
		u := a.Cfg.ManageURL
		if u == "" {
			u = billing.ManageSubscriptionURL
		}
		out.ManageSubscription = &u
	}
	out.Allowed = make([]string, 0, len(pl.Allowed))
	for _, act := range pl.Allowed {
		if dropUpload && act == billing.ActUpload {
			continue
		}
		out.Allowed = append(out.Allowed, act)
	}
	return out
}

// accountBase builds the account object without usage and limits. withUsage looks the household's
// usage up so that plan.allowed can drop "upload" at a hard limit (design ask 5).
func (a *API) accountBase(ctx context.Context, p *principal) (accountOut, *usageOut, *limits.Limits, error) {
	var usage *usageOut
	var lim *limits.Limits
	drop := false
	if p.HH.ID != "" {
		l, err := a.Meter.LimitsFor(ctx, p.HH.ID)
		if err != nil {
			return accountOut{}, nil, nil, err
		}
		queued, _ := a.queuedJobs(ctx, p.HH.ID)
		u, err := a.Meter.UsageFor(ctx, p.HH.ID, queued)
		if err != nil {
			return accountOut{}, nil, nil, err
		}
		lim = &l
		usage = &usageOut{Usage: u, ReceiptsSoftReached: l.ReceiptsPerMonthSoft > 0 && u.ReceiptsThisMonth >= l.ReceiptsPerMonthSoft}
		drop = a.uploadBlocked(ctx, p.HH.ID, l, u)
	}
	out := accountOut{ID: p.Acc.ID, DisplayName: sptr(p.Acc.DisplayName), Email: sptr(p.Acc.Email), EmailIsPrivate: p.Acc.EmailIsPrivate,
		AppAccountToken: p.Acc.AppAccountToken, SupportCode: p.Acc.SupportCode, Role: p.Role, HouseholdID: sptr(p.HH.ID),
		CreatedAt: p.Acc.CreatedAt, Plan: a.planView(p, drop)}
	return out, usage, lim, nil
}

// accountShort is the account object used in sign-in, billing and invite responses (no usage, no limits).
func (a *API) accountShort(ctx context.Context, p *principal) (accountOut, error) {
	out, _, _, err := a.accountBase(ctx, p)
	return out, err
}

// accountFull is GET /v1/account.
func (a *API) accountFullView(ctx context.Context, p *principal) (accountFull, error) {
	base, usage, lim, err := a.accountBase(ctx, p)
	if err != nil {
		return accountFull{}, err
	}
	out := accountFull{accountOut: base}
	if usage != nil {
		out.Usage = usage
		out.Limits = lim.Public()
	}
	return out, nil
}

// refreshPrincipal reloads household and plan after a change.
func (a *API) refreshPrincipal(ctx context.Context, p *principal) (*principal, error) {
	acc, err := a.Store.AccountByID(ctx, p.Acc.ID)
	if err != nil {
		return nil, err
	}
	np := &principal{Acc: acc, Dev: p.Dev, Role: "none", IP: p.IP}
	h, m, err := a.Store.HouseholdOfAccount(ctx, acc.ID)
	switch {
	case err == nil:
		np.HH, np.Role = h, m.Role
	case control.Is(err, control.ErrNoHousehold):
	default:
		return nil, err
	}
	np.Plan, err = a.Calc.ForAccount(ctx, acc.ID)
	return np, err
}

type memberOut struct {
	AccountID   string    `json:"account_id"`
	DisplayName *string   `json:"display_name"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
	IsYou       bool      `json:"is_you"`
}

type inviteOut struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type householdOut struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	OwnerAccountID      string       `json:"owner_account_id"`
	MemberCap           int          `json:"member_cap"`
	YourRole            string       `json:"your_role"`
	CreatedAt           time.Time    `json:"created_at"`
	Members             []memberOut  `json:"members"`
	PendingInvites      *[]inviteOut `json:"pending_invites,omitempty"`
	PendingInvitesCount *int         `json:"pending_invites_count,omitempty"`
}

// householdView renders the caller's household. withInvites adds pending_invites (owner only) and
// pending_invites_count (everyone: design ask 3, so a member's seat meter matches the owner's).
func (a *API) householdView(ctx context.Context, accountID string, withInvites bool) (householdOut, error) {
	v, err := a.Ctl.ViewHousehold(ctx, accountID)
	if err != nil {
		return householdOut{}, err
	}
	out := householdOut{ID: v.Household.ID, Name: v.Household.Name, OwnerAccountID: v.Household.OwnerAccountID,
		MemberCap: a.Ctl.Cfg.MemberCap, YourRole: v.YourRole, CreatedAt: v.Household.CreatedAt, Members: []memberOut{}}
	for _, m := range v.Members {
		out.Members = append(out.Members, memberOut{AccountID: m.AccountID, DisplayName: sptr(m.DisplayName), Role: m.Role, JoinedAt: m.JoinedAt, IsYou: m.AccountID == accountID})
	}
	if withInvites {
		open, err := a.Store.OpenInvites(ctx, v.Household.ID, a.Store.Now())
		if err != nil {
			return out, err
		}
		n := len(open)
		out.PendingInvitesCount = &n
		if v.YourRole == control.RoleOwner {
			list := make([]inviteOut, 0, n)
			for _, i := range v.PendingInvites {
				list = append(list, inviteOut{i.ID, i.CreatedAt, i.ExpiresAt})
			}
			out.PendingInvites = &list
		}
	}
	return out, nil
}

type deviceOut struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Current    bool       `json:"current"`
}

func deviceView(d control.Device, current int64) deviceOut {
	return deviceOut{ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, LastUsedAt: tptr(d.LastUsedAt), Current: d.ID == current}
}

// buildInfoLimits renders the default limits as the null-for-unlimited object.
func (a *API) buildInfoLimits() map[string]any {
	b, _ := json.Marshal(a.Cfg.Limits.Public())
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}
