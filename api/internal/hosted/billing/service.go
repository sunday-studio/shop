package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"shop/internal/hosted/control"
)

// Errors with contract codes. Compare with control.Is.
var (
	ErrNotificationInvalid = &control.Error{Code: "apple_token_invalid", Status: http.StatusBadRequest, Msg: "signed payload is invalid"}
	ErrTransactionInvalid  = &control.Error{Code: "apple_transaction_invalid", Status: http.StatusBadRequest, Msg: "signed transaction is invalid"}
	ErrProductNotAllowed   = &control.Error{Code: "product_not_allowed", Status: http.StatusBadRequest, Msg: "product is not allowed"}
	ErrBelongsToOther      = &control.Error{Code: "subscription_belongs_to_other_account", Status: http.StatusConflict, Msg: "This subscription belongs to a different Shop sign-in."}
	ErrUnavailable         = &control.Error{Code: "apple_unavailable", Status: http.StatusBadGateway, Msg: "Apple is unavailable"}
)

// Config of the billing service.
type Config struct {
	BundleID      string
	ProductIDs    []string      // allow-list (contract: billing.product_ids)
	AllowLocal    bool          // accept Xcode / LocalTesting environments (dev stacks only)
	GraceFallback time.Duration // grace length when Apple does not say (16 days)
}

// Service verifies Apple data and keeps the entitlements table in step with it.
type Service struct {
	Ctl *control.Service
	V   *Verifier
	API ServerAPI
	Cfg Config
	Log *slog.Logger
}

// NewService wires the service. api may be nil (then Unavailable is used: Apple is never called).
func NewService(ctl *control.Service, v *Verifier, api ServerAPI, cfg Config) *Service {
	if api == nil {
		api = Unavailable{}
	}
	if cfg.GraceFallback == 0 {
		cfg.GraceFallback = 16 * 24 * time.Hour
	}
	return &Service{Ctl: ctl, V: v, API: api, Cfg: cfg, Log: slog.Default()}
}

func (s *Service) productAllowed(id string) bool {
	for _, p := range s.Cfg.ProductIDs {
		if p == id {
			return true
		}
	}
	return false
}

func (s *Service) envOK(env string) bool {
	switch env {
	case EnvProduction, EnvSandbox, "":
		return true
	}
	return s.Cfg.AllowLocal
}

// ---- notification outcomes ----

const (
	OutcomeApplied   = "applied"
	OutcomeDuplicate = "duplicate"
	OutcomeStale     = "stale"    // older than data we already hold: recorded, not applied
	OutcomeIgnored   = "ignored"  // other bundle id or product
	OutcomeRecorded  = "recorded" // known type with no state change (logged)
)

// NotificationResult tells the caller what happened (the HTTP layer answers 200 for every Outcome).
type NotificationResult struct {
	UUID                  string
	Type                  string
	Subtype               string
	Outcome               string
	OriginalTransactionID string
	AccountID             string
}

// ProcessNotification verifies a signedPayload and applies it exactly once. Errors that wrap
// ErrNotificationInvalid mean HTTP 400; any other error means HTTP 500 (Apple retries) and
// nothing was recorded.
func (s *Service) ProcessNotification(ctx context.Context, signedPayload string) (NotificationResult, error) {
	var res NotificationResult
	var n NotificationPayload
	if err := s.V.Verify(signedPayload, &n); err != nil {
		return res, fmt.Errorf("%w: %v", ErrNotificationInvalid, err)
	}
	if n.NotificationUUID == "" || n.NotificationType == "" {
		return res, fmt.Errorf("%w: missing type or uuid", ErrNotificationInvalid)
	}
	res = NotificationResult{UUID: n.NotificationUUID, Type: n.NotificationType, Subtype: n.Subtype}
	if n.Data.BundleID != "" && n.Data.BundleID != s.Cfg.BundleID {
		res.Outcome = OutcomeIgnored
		s.Log.Info("billing: notification for another bundle id ignored", "uuid", n.NotificationUUID, "bundle", n.Data.BundleID)
		return res, nil
	}
	var tx *TransactionInfo
	var ren *RenewalInfo
	if n.Data.SignedTransactionInfo != "" {
		tx = &TransactionInfo{}
		if err := s.V.Verify(n.Data.SignedTransactionInfo, tx); err != nil {
			return res, fmt.Errorf("%w: transaction info: %v", ErrNotificationInvalid, err)
		}
		res.OriginalTransactionID = tx.OriginalTransactionID
	}
	if n.Data.SignedRenewalInfo != "" {
		ren = &RenewalInfo{}
		if err := s.V.Verify(n.Data.SignedRenewalInfo, ren); err != nil {
			return res, fmt.Errorf("%w: renewal info: %v", ErrNotificationInvalid, err)
		}
	}
	err := s.Ctl.S.Tx(ctx, func(st *control.Store) error {
		inserted, err := st.InsertNotification(ctx, control.NotificationRecord{
			UUID: n.NotificationUUID, Type: n.NotificationType, Subtype: n.Subtype,
			OriginalTransactionID: res.OriginalTransactionID, SignedDateMs: int64(n.SignedDate),
		})
		if err != nil {
			return err
		}
		if !inserted {
			res.Outcome = OutcomeDuplicate
			return nil
		}
		outcome, acc, err := s.applyNotification(ctx, st, n, tx, ren)
		if err != nil {
			return err
		}
		res.Outcome, res.AccountID = outcome, acc
		return st.SetNotificationResult(ctx, n.NotificationUUID, outcome)
	})
	if err != nil {
		return NotificationResult{}, err
	}
	s.Log.Info("billing: notification", "uuid", res.UUID, "type", res.Type, "subtype", res.Subtype, "outcome", res.Outcome)
	return res, nil
}

func (s *Service) applyNotification(ctx context.Context, st *control.Store, n NotificationPayload, tx *TransactionInfo, ren *RenewalInfo) (outcome, accountID string, err error) {
	if n.NotificationType == NtTest || tx == nil {
		return OutcomeRecorded, "", nil // TEST, CONSUMPTION_REQUEST and other payloads without a subscription
	}
	if (tx.BundleID != "" && tx.BundleID != s.Cfg.BundleID) || !s.productAllowed(tx.ProductID) || tx.OriginalTransactionID == "" {
		return OutcomeIgnored, "", nil
	}
	e, err := st.EntitlementByOriginalTx(ctx, tx.OriginalTransactionID)
	isNew := errors.Is(err, control.ErrNotFound)
	if err != nil && !isNew {
		return "", "", err
	}
	if isNew {
		e = control.Entitlement{Source: control.SourceApple, Key: control.KeyHosted, OriginalTransactionID: tx.OriginalTransactionID}
	} else if e.LastSignedMs > int64(n.SignedDate) {
		return OutcomeStale, e.AccountID, nil
	}
	if err := s.bindByToken(ctx, st, &e, tx); err != nil {
		return "", "", err
	}
	prevState := e.State
	fillFromTx(&e, tx, ren, n.Data.Environment)
	changed := true
	switch n.NotificationType {
	case NtSubscribed, NtDidRenew, NtRefundReversed:
		e.State, e.GraceUntil, e.RevokedAt = control.EntActive, time.Time{}, time.Time{}
	case NtDidFailToRenew:
		if n.Subtype == SubGracePeriod {
			e.State = control.EntGrace
			if ren != nil && ren.GracePeriodExpiresDate != 0 {
				e.GraceUntil = ren.GracePeriodExpiresDate.Time()
			} else {
				e.GraceUntil = e.ExpiresAt.Add(s.Cfg.GraceFallback)
			}
		} else {
			e.State = control.EntBillingRetry
		}
	case NtGracePeriodExpired:
		e.State = control.EntBillingRetry
	case NtExpired:
		e.State = control.EntExpired
	case NtRefund, NtRevoke:
		e.State = control.EntRevoked
		e.RevokedAt = tx.RevocationDate.Time()
		if e.RevokedAt.IsZero() {
			e.RevokedAt = n.SignedDate.Time()
		}
	case NtDidChangeRenewalStatus:
		switch n.Subtype {
		case SubAutoRenewEnabled:
			t := true
			e.AutoRenew = &t
		case SubAutoRenewDisabled:
			f := false
			e.AutoRenew = &f
		}
		changed = false
	default:
		// OFFER_REDEEMED, PRICE_INCREASE, DID_CHANGE_RENEWAL_PREF, RENEWAL_EXTENDED, REFUND_DECLINED ...:
		// dates and flags are refreshed above, the state is not touched.
		changed = false
	}
	if isNew && !changed {
		e.State = stateFromStatusCode(n.Data.Status)
	}
	markEnded(&e, prevState, n.SignedDate.Time())
	e.LastSignedMs = int64(n.SignedDate)
	if e, err = st.UpsertEntitlement(ctx, e); err != nil {
		return "", "", err
	}
	if err := s.afterUpsert(ctx, st, e, "apple:"+n.NotificationType); err != nil {
		return "", "", err
	}
	if !changed && !isNew {
		s.Log.Info("billing: notification recorded without state change", "type", n.NotificationType, "subtype", n.Subtype)
		return OutcomeRecorded, e.AccountID, nil
	}
	return OutcomeApplied, e.AccountID, nil
}

// bindByToken links an unclaimed entitlement to the account named by the appAccountToken.
func (s *Service) bindByToken(ctx context.Context, st *control.Store, e *control.Entitlement, tx *TransactionInfo) error {
	if tx.AppAccountToken == "" {
		return nil
	}
	acc, err := st.AccountByAppToken(ctx, tx.AppAccountToken)
	if errors.Is(err, control.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.AccountID == "" {
		e.AccountID = acc.ID
	} else if e.AccountID != acc.ID {
		s.Log.Warn("billing: appAccountToken differs from the bound account; keeping the first claim", "original_transaction_id", e.OriginalTransactionID)
		return st.AppendAudit(ctx, "system", "billing.token_conflict", e.AccountID, map[string]any{"original_transaction_id": e.OriginalTransactionID, "token_account": acc.ID})
	}
	return nil
}

// afterUpsert creates the household once a bound entitlement can give access and writes the audit row.
func (s *Service) afterUpsert(ctx context.Context, st *control.Store, e control.Entitlement, action string) error {
	subject := e.AccountID
	if subject == "" {
		subject = "unclaimed:" + e.OriginalTransactionID
	}
	if e.AccountID != "" && (e.State == control.EntActive || e.State == control.EntGrace || e.State == control.EntBillingRetry) {
		if _, _, err := s.Ctl.WithStore(st).EnsureHousehold(ctx, e.AccountID); err != nil && !control.Is(err, control.ErrAccountLocked) {
			return err
		}
	}
	return st.AppendAudit(ctx, "system", "billing."+strings.ToLower(action), subject, map[string]any{"state": e.State, "original_transaction_id": e.OriginalTransactionID, "expires_at": control.FormatTime(e.ExpiresAt)})
}

func fillFromTx(e *control.Entitlement, tx *TransactionInfo, ren *RenewalInfo, dataEnv string) {
	e.ProductID = tx.ProductID
	env := tx.Environment
	if env == "" {
		env = dataEnv
	}
	if env != "" {
		e.Environment = strings.ToLower(env)
	}
	e.LatestTransactionID = tx.TransactionID
	e.OfferType, e.OfferID = tx.OfferType, tx.OfferIdentifier
	if t := tx.OriginalPurchaseDate.Time(); !t.IsZero() {
		e.StartsAt = t
	}
	if t := tx.ExpiresDate.Time(); !t.IsZero() {
		e.ExpiresAt = t
	}
	e.IsTrial = tx.IsFreeTrial()
	if ren != nil {
		b := ren.AutoRenewStatus == 1
		e.AutoRenew = &b
		if ren.GracePeriodExpiresDate != 0 {
			e.GraceUntil = ren.GracePeriodExpiresDate.Time()
		}
	}
}

func stateFromStatusCode(code int) string {
	switch code {
	case appleStatusExpired:
		return control.EntExpired
	case appleStatusRetry:
		return control.EntBillingRetry
	case appleStatusGrace:
		return control.EntGrace
	case appleStatusRevoked:
		return control.EntRevoked
	default:
		return control.EntActive
	}
}

// applySnapshot writes an authoritative Server API status into the entitlement. It reports
// false (and changes nothing) when the snapshot is older than data already held.
func (s *Service) applySnapshot(e *control.Entitlement, st SubscriptionStatus) bool {
	signed := int64(st.Transaction.SignedDate)
	if int64(st.Renewal.SignedDate) > signed {
		signed = int64(st.Renewal.SignedDate)
	}
	if signed == 0 {
		signed = s.Ctl.S.Now().UnixMilli()
	}
	if signed < e.LastSignedMs {
		return false
	}
	ren := st.Renewal
	prevState := e.State
	fillFromTx(e, &st.Transaction, &ren, st.Environment)
	e.State = stateFromStatusCode(st.Status)
	defer markEnded(e, prevState, time.UnixMilli(signed).UTC())
	switch e.State {
	case control.EntActive:
		e.GraceUntil, e.RevokedAt = time.Time{}, time.Time{}
	case control.EntGrace:
		if st.Renewal.GracePeriodExpiresDate == 0 {
			e.GraceUntil = e.ExpiresAt.Add(s.Cfg.GraceFallback)
		}
	case control.EntRevoked:
		e.RevokedAt = st.Transaction.RevocationDate.Time()
		if e.RevokedAt.IsZero() {
			e.RevokedAt = s.Ctl.S.Now()
		}
	}
	e.LastSignedMs = signed
	return true
}

// ---- POST /v1/billing/apple/transactions ----

// Bind results.
const (
	ResultBound        = "bound"
	ResultAlreadyBound = "already_bound"
	ResultClaimed      = "claimed"
)

// BindResult is the outcome of BindTransaction.
type BindResult struct {
	Result      string
	Entitlement control.Entitlement
	Household   control.Household
}

// BindTransaction verifies a signed transaction from the app, links it to the calling account
// (appAccountToken, or Set App Account Token for an unclaimed offer-code purchase), reads the
// authoritative status from Apple, stores it, and creates the household on the first entitlement.
func (s *Service) BindTransaction(ctx context.Context, accountID, signedTransaction string) (BindResult, error) {
	var out BindResult
	var tx TransactionInfo
	if err := s.V.Verify(signedTransaction, &tx); err != nil {
		return out, fmt.Errorf("%w: %v", ErrTransactionInvalid, err)
	}
	if tx.BundleID != s.Cfg.BundleID || tx.OriginalTransactionID == "" || !s.envOK(tx.Environment) {
		return out, fmt.Errorf("%w: bundle id, environment or ids", ErrTransactionInvalid)
	}
	if !s.productAllowed(tx.ProductID) {
		return out, ErrProductNotAllowed
	}
	acc, err := s.Ctl.S.AccountByID(ctx, accountID)
	if err != nil {
		return out, err
	}
	if acc.Status != control.AccountActive {
		return out, control.ErrAccountLocked
	}
	existing, err := s.Ctl.S.EntitlementByOriginalTx(ctx, tx.OriginalTransactionID)
	isNew := errors.Is(err, control.ErrNotFound)
	if err != nil && !isNew {
		return out, err
	}
	tokenOK := tx.AppAccountToken != "" && strings.EqualFold(tx.AppAccountToken, acc.AppAccountToken)
	if tx.AppAccountToken != "" && !tokenOK {
		return out, ErrBelongsToOther
	}
	if !isNew && existing.AccountID != "" && existing.AccountID != acc.ID {
		return out, ErrBelongsToOther
	}
	result := ResultBound
	switch {
	case !isNew && existing.AccountID == acc.ID:
		result = ResultAlreadyBound
	case !tokenOK:
		result = ResultClaimed // unclaimed purchase (typically an offer code): bind through Apple
	case !isNew && existing.AccountID == "":
		result = ResultClaimed
	}
	status, err := s.API.GetSubscriptionStatus(ctx, tx.OriginalTransactionID)
	if err != nil {
		return out, mapAPIError(err)
	}
	if status.Transaction.OriginalTransactionID != "" && status.Transaction.OriginalTransactionID != tx.OriginalTransactionID {
		return out, fmt.Errorf("%w: Apple answered for another subscription", ErrTransactionInvalid)
	}
	if result == ResultClaimed && !tokenOK {
		if err := s.API.SetAppAccountToken(ctx, tx.OriginalTransactionID, acc.AppAccountToken); err != nil {
			return out, mapAPIError(err)
		}
	}
	err = s.Ctl.S.Tx(ctx, func(st *control.Store) error {
		e, err := st.EntitlementByOriginalTx(ctx, tx.OriginalTransactionID)
		if errors.Is(err, control.ErrNotFound) {
			e = control.Entitlement{Source: control.SourceApple, Key: control.KeyHosted, OriginalTransactionID: tx.OriginalTransactionID}
		} else if err != nil {
			return err
		}
		if e.AccountID != "" && e.AccountID != acc.ID {
			return ErrBelongsToOther // lost a race with another account
		}
		e.AccountID = acc.ID
		if !s.applySnapshot(&e, status) && e.ID == "" {
			return fmt.Errorf("%w: stale snapshot for a new entitlement", ErrTransactionInvalid)
		}
		if e, err = st.UpsertEntitlement(ctx, e); err != nil {
			return err
		}
		if err := s.afterUpsert(ctx, st, e, "bind_"+result); err != nil {
			return err
		}
		out.Entitlement = e
		h, _, herr := st.HouseholdOfAccount(ctx, acc.ID)
		if herr == nil {
			out.Household = h
		}
		return nil
	})
	out.Result = result
	return out, err
}

func mapAPIError(err error) error {
	if errors.Is(err, ErrAppleUnavailable) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if errors.Is(err, ErrAppleNotFound) {
		return fmt.Errorf("%w: Apple does not know this transaction", ErrTransactionInvalid)
	}
	return err
}

// ---- reconcile / refresh ----

// ReconcileOriginal re-reads one subscription from Apple and stores the answer.
func (s *Service) ReconcileOriginal(ctx context.Context, originalTransactionID string) error {
	status, err := s.API.GetSubscriptionStatus(ctx, originalTransactionID)
	if err != nil {
		return mapAPIError(err)
	}
	return s.Ctl.S.Tx(ctx, func(st *control.Store) error {
		e, err := st.EntitlementByOriginalTx(ctx, originalTransactionID)
		if err != nil {
			return err
		}
		if !s.applySnapshot(&e, status) {
			return nil
		}
		e, err = st.UpsertEntitlement(ctx, e)
		if err != nil {
			return err
		}
		return s.afterUpsert(ctx, st, e, "reconcile")
	})
}

// Refresh re-reads every Apple subscription of the account (POST /v1/billing/apple/refresh,
// and the fresh check before a deletion). It returns the number of subscriptions read.
func (s *Service) Refresh(ctx context.Context, accountID string) (int, error) {
	ents, err := s.Ctl.S.EntitlementsForAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if e.Source != control.SourceApple || e.OriginalTransactionID == "" {
			continue
		}
		if err := s.ReconcileOriginal(ctx, e.OriginalTransactionID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// markEnded records, once, when a subscription was first seen expired: the later of its period
// end and the moment Apple said so. That is when the 30 day lapse clock starts (after a billing
// retry that can be well after the period end). Later reconciles never move it.
func markEnded(e *control.Entitlement, prev string, signed time.Time) {
	if e.State != control.EntExpired {
		e.EndedAt = time.Time{}
		return
	}
	if prev == control.EntExpired && !e.EndedAt.IsZero() {
		return
	}
	e.EndedAt = e.ExpiresAt
	if signed.After(e.EndedAt) {
		e.EndedAt = signed
	}
}
