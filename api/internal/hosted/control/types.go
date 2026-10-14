package control

import "time"

// Account statuses.
const (
	AccountActive   = "active"
	AccountDisabled = "disabled"
	AccountLocked   = "locked"
	AccountDeleted  = "deleted"
)

// Household statuses.
const (
	HouseholdActive     = "active"
	HouseholdTombstoned = "tombstoned" // stage 1 of deletion done, files still on disk
	HouseholdDeleted    = "deleted"    // stage 2 done, files erased
)

// Roles.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Entitlement sources and states.
const (
	SourceApple = "apple"
	SourceGrant = "grant"

	EntActive       = "active"
	EntGrace        = "grace"
	EntBillingRetry = "billing_retry"
	EntExpired      = "expired"
	EntRevoked      = "revoked"
)

// KeyHosted is the entitlement key of the base plan.
const KeyHosted = "hosted"

// Zero times mean "not set" everywhere in these types.

type Account struct {
	ID                  string
	AppleSub            string
	DisplayName         string
	Email               string
	EmailIsPrivate      bool
	EmailDeliverable    bool
	AppAccountToken     string
	SupportCode         string
	Status              string
	RefreshTokenEnc     []byte
	CreatedAt           time.Time
	LastLoginAt         time.Time
	DeletionRequestedAt time.Time
	DeletedAt           time.Time
}

type Household struct {
	ID                  string
	OwnerAccountID      string
	Name                string
	Status              string
	CreatedAt           time.Time
	TombstonedAt        time.Time
	DeletedAt           time.Time
	DeletionRequestedAt time.Time // user asked to delete the owner account (7 day cooling-off)
	ForcedLapseAt       time.Time // Apple account-deleted: the lapse clock starts here
	PlanStateCache      string
	ReadOnlySince       time.Time
	DeleteAfter         time.Time
	LimitsOverride      string // JSON, see limits package
}

type Member struct {
	HouseholdID string
	AccountID   string
	Role        string
	JoinedAt    time.Time
	DisplayName string
}

type Invite struct {
	ID          string
	HouseholdID string
	CodeHash    string
	CreatedBy   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	UsedBy      string
	UsedAt      time.Time
	RevokedAt   time.Time
}

// Open reports whether the invite can still be accepted at now.
func (i Invite) Open(now time.Time) bool {
	return i.UsedAt.IsZero() && i.RevokedAt.IsZero() && now.Before(i.ExpiresAt)
}

type Device struct {
	ID         int64
	AccountID  string
	TokenHash  string
	Name       string
	Kind       string
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

type Entitlement struct {
	ID                    string
	AccountID             string // "" = unclaimed
	Key                   string
	Source                string
	State                 string
	IsTrial               bool
	StartsAt              time.Time
	ExpiresAt             time.Time
	GraceUntil            time.Time
	AutoRenew             *bool
	ProductID             string
	Environment           string
	OriginalTransactionID string
	LatestTransactionID   string
	OfferType             int
	OfferID               string
	GrantID               string
	RevokedAt             time.Time
	EndedAt               time.Time // set once, when the subscription is first seen expired
	LastSignedMs          int64
	UpdatedAt             time.Time
}

type Grant struct {
	ID             string
	AccountID      string
	Key            string
	ExpiresAt      time.Time
	Reason         string
	CreatedBy      string
	CreatedAt      time.Time
	RevokedAt      time.Time
	RevokedBy      string
	RevokedReason  string
	IdempotencyKey string
}

type AuditEntry struct {
	ID      int64
	At      time.Time
	Actor   string
	Action  string
	Subject string
	Details string // JSON
}

type NotificationRecord struct {
	UUID                  string
	Type                  string
	Subtype               string
	OriginalTransactionID string
	SignedDateMs          int64
	ReceivedAt            time.Time
	Result                string
}

type LifecycleEvent struct {
	ID          int64
	HouseholdID string
	Kind        string
	Anchor      string
	DueAt       time.Time
	CreatedAt   time.Time
}

type PendingRevocation struct {
	AccountID string
	TokenEnc  []byte
	CreatedAt time.Time
	Attempts  int
	LastError string
}
