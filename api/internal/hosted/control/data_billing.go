package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ---- entitlements ----

const entCols = `id, account_id, key, source, state, is_trial, starts_at, expires_at, grace_until, auto_renew, product_id, environment, original_transaction_id, latest_transaction_id, offer_type, offer_id, grant_id, revoked_at, last_signed_ms, updated_at`

func scanEnt(r scanner) (Entitlement, error) {
	var e Entitlement
	var acc, sa, ea, gu, otx, gid, ra, ua sql.NullString
	var tr int
	var ar sql.NullInt64
	err := r.Scan(&e.ID, &acc, &e.Key, &e.Source, &e.State, &tr, &sa, &ea, &gu, &ar, &e.ProductID, &e.Environment, &otx, &e.LatestTransactionID, &e.OfferType, &e.OfferID, &gid, &ra, &e.LastSignedMs, &ua)
	if err != nil {
		return e, err
	}
	e.AccountID, e.IsTrial = acc.String, tr == 1
	e.StartsAt, e.ExpiresAt, e.GraceUntil, e.RevokedAt, e.UpdatedAt = pt(sa), pt(ea), pt(gu), pt(ra), pt(ua)
	if ar.Valid {
		b := ar.Int64 == 1
		e.AutoRenew = &b
	}
	e.OriginalTransactionID, e.GrantID = otx.String, gid.String
	return e, nil
}

// UpsertEntitlement inserts or fully replaces the entitlement with the same id.
func (s *Store) UpsertEntitlement(ctx context.Context, e Entitlement) (Entitlement, error) {
	if e.ID == "" {
		e.ID = NewUUID()
	}
	if e.Key == "" {
		e.Key = KeyHosted
	}
	e.UpdatedAt = s.Now()
	var ar any
	if e.AutoRenew != nil {
		ar = b2i(*e.AutoRenew)
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO entitlements (`+entCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id, key=excluded.key, source=excluded.source, state=excluded.state,
		is_trial=excluded.is_trial, starts_at=excluded.starts_at, expires_at=excluded.expires_at, grace_until=excluded.grace_until,
		auto_renew=excluded.auto_renew, product_id=excluded.product_id, environment=excluded.environment,
		original_transaction_id=excluded.original_transaction_id, latest_transaction_id=excluded.latest_transaction_id,
		offer_type=excluded.offer_type, offer_id=excluded.offer_id, grant_id=excluded.grant_id, revoked_at=excluded.revoked_at,
		last_signed_ms=excluded.last_signed_ms, updated_at=excluded.updated_at`,
		e.ID, nullStr(e.AccountID), e.Key, e.Source, e.State, b2i(e.IsTrial), nts(e.StartsAt), nts(e.ExpiresAt), nts(e.GraceUntil), ar,
		e.ProductID, e.Environment, nullStr(e.OriginalTransactionID), e.LatestTransactionID, e.OfferType, e.OfferID, nullStr(e.GrantID),
		nts(e.RevokedAt), e.LastSignedMs, ts(e.UpdatedAt))
	return e, err
}

func (s *Store) EntitlementByOriginalTx(ctx context.Context, otx string) (Entitlement, error) {
	e, err := scanEnt(s.q.QueryRowContext(ctx, `SELECT `+entCols+` FROM entitlements WHERE original_transaction_id = ?`, otx))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

func (s *Store) EntitlementByGrant(ctx context.Context, grantID string) (Entitlement, error) {
	e, err := scanEnt(s.q.QueryRowContext(ctx, `SELECT `+entCols+` FROM entitlements WHERE grant_id = ?`, grantID))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// EntitlementsForAccount lists every entitlement of the account (all sources), oldest first.
func (s *Store) EntitlementsForAccount(ctx context.Context, accountID string) ([]Entitlement, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+entCols+` FROM entitlements WHERE account_id = ? ORDER BY updated_at, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entitlement
	for rows.Next() {
		e, err := scanEnt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- grants ----

const grantCols = `id, account_id, key, expires_at, reason, created_by, created_at, revoked_at, revoked_by, revoked_reason, idempotency_key`

func scanGrant(r scanner) (Grant, error) {
	var g Grant
	var ea, ca, ra, rb, rr, ik sql.NullString
	err := r.Scan(&g.ID, &g.AccountID, &g.Key, &ea, &g.Reason, &g.CreatedBy, &ca, &ra, &rb, &rr, &ik)
	g.ExpiresAt, g.CreatedAt, g.RevokedAt = pt(ea), pt(ca), pt(ra)
	g.RevokedBy, g.RevokedReason, g.IdempotencyKey = rb.String, rr.String, ik.String
	return g, err
}

func (s *Store) InsertGrant(ctx context.Context, g Grant) (Grant, error) {
	if g.ID == "" {
		g.ID = NewUUID()
	}
	if g.Key == "" {
		g.Key = KeyHosted
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = s.Now()
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO grants (id, account_id, key, expires_at, reason, created_by, created_at, idempotency_key) VALUES (?,?,?,?,?,?,?,?)`,
		g.ID, g.AccountID, g.Key, ts(g.ExpiresAt), g.Reason, g.CreatedBy, ts(g.CreatedAt), nullStr(g.IdempotencyKey))
	return g, err
}

func (s *Store) GrantByID(ctx context.Context, id string) (Grant, error) {
	g, err := scanGrant(s.q.QueryRowContext(ctx, `SELECT `+grantCols+` FROM grants WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

func (s *Store) GrantByIdempotencyKey(ctx context.Context, key string) (Grant, error) {
	g, err := scanGrant(s.q.QueryRowContext(ctx, `SELECT `+grantCols+` FROM grants WHERE idempotency_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

// ListGrants returns grants newest first, optionally for one account.
func (s *Store) ListGrants(ctx context.Context, accountID string) ([]Grant, error) {
	q := `SELECT ` + grantCols + ` FROM grants`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	rows, err := s.q.QueryContext(ctx, q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) SetGrantExpiry(ctx context.Context, id string, expires time.Time) error {
	return s.execOne(ctx, `UPDATE grants SET expires_at = ? WHERE id = ?`, ts(expires), id)
}

func (s *Store) RevokeGrant(ctx context.Context, id, by, reason string, at time.Time) error {
	return s.execOne(ctx, `UPDATE grants SET revoked_at = ?, revoked_by = ?, revoked_reason = ? WHERE id = ? AND revoked_at IS NULL`, ts(at), by, reason, id)
}

// CountActiveGrants counts grants that are neither revoked nor expired at now.
func (s *Store) CountActiveGrants(ctx context.Context, now time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM grants WHERE revoked_at IS NULL AND expires_at > ?`, ts(now)).Scan(&n)
	return n, err
}

// ---- usage counters ----

// Counter returns the value of a counter (0 when absent).
func (s *Store) Counter(ctx context.Context, householdID, period, metric string) (int64, error) {
	var v int64
	err := s.q.QueryRowContext(ctx, `SELECT value FROM usage_counters WHERE household_id = ? AND period = ? AND metric = ?`, householdID, period, metric).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// AddCounter adds delta (may be negative, floor 0) and returns the new value.
func (s *Store) AddCounter(ctx context.Context, householdID, period, metric string, delta int64) (int64, error) {
	var v int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO usage_counters (household_id, period, metric, value) VALUES (?,?,?,MAX(0,?))
		ON CONFLICT(household_id, period, metric) DO UPDATE SET value = MAX(0, value + ?) RETURNING value`,
		householdID, period, metric, delta, delta).Scan(&v)
	return v, err
}

// SetCounter sets a gauge.
func (s *Store) SetCounter(ctx context.Context, householdID, period, metric string, value int64) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO usage_counters (household_id, period, metric, value) VALUES (?,?,?,?)
		ON CONFLICT(household_id, period, metric) DO UPDATE SET value = excluded.value`, householdID, period, metric, value)
	return err
}

// DeleteHouseholdCounters removes every counter of a household (erasure).
func (s *Store) DeleteHouseholdCounters(ctx context.Context, householdID string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM usage_counters WHERE household_id = ?`, householdID)
	return err
}

// ---- audit ----

// AppendAudit writes an append-only audit row. details is marshalled to JSON.
func (s *Store) AppendAudit(ctx context.Context, actor, action, subject string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = s.q.ExecContext(ctx, `INSERT INTO audit_log (at, actor, action, subject, details) VALUES (?,?,?,?,?)`, ts(s.Now()), actor, action, subject, string(b))
	return err
}

// AuditFilter narrows ListAudit.
type AuditFilter struct {
	Subject string
	Action  string
	Limit   int
}

// ListAudit returns entries newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := s.q.QueryContext(ctx, `SELECT id, at, actor, action, subject, details FROM audit_log
		WHERE (? = '' OR subject = ?) AND (? = '' OR action = ?) ORDER BY id DESC LIMIT ?`, f.Subject, f.Subject, f.Action, f.Action, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		var at sql.NullString
		if err := rows.Scan(&a.ID, &at, &a.Actor, &a.Action, &a.Subject, &a.Details); err != nil {
			return nil, err
		}
		a.At = pt(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- processed notifications (idempotency) ----

// InsertNotification records a notification id; inserted is false for a duplicate.
func (s *Store) InsertNotification(ctx context.Context, n NotificationRecord) (inserted bool, err error) {
	res, err := s.q.ExecContext(ctx, `INSERT OR IGNORE INTO processed_notifications (notification_uuid, type, subtype, original_transaction_id, signed_date_ms, received_at, result) VALUES (?,?,?,?,?,?,?)`,
		n.UUID, n.Type, n.Subtype, n.OriginalTransactionID, n.SignedDateMs, ts(s.Now()), n.Result)
	if err != nil {
		return false, err
	}
	c, _ := res.RowsAffected()
	return c == 1, nil
}

func (s *Store) SetNotificationResult(ctx context.Context, uuid, result string) error {
	return s.execOne(ctx, `UPDATE processed_notifications SET result = ? WHERE notification_uuid = ?`, result, uuid)
}

func (s *Store) NotificationByUUID(ctx context.Context, uuid string) (NotificationRecord, error) {
	var n NotificationRecord
	var ra sql.NullString
	err := s.q.QueryRowContext(ctx, `SELECT notification_uuid, type, subtype, original_transaction_id, signed_date_ms, received_at, result FROM processed_notifications WHERE notification_uuid = ?`, uuid).
		Scan(&n.UUID, &n.Type, &n.Subtype, &n.OriginalTransactionID, &n.SignedDateMs, &ra, &n.Result)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	n.ReceivedAt = pt(ra)
	return n, err
}

// ---- Sign in with Apple nonces ----

func (s *Store) InsertNonce(ctx context.Context, hash string, expires time.Time) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO siwa_nonces (nonce_hash, expires_at) VALUES (?,?)`, hash, ts(expires))
	return err
}

// ConsumeNonce marks the nonce used and reports whether it was known, unused and unexpired.
// The nonce is consumed whenever it is found, even if it turns out to be expired.
func (s *Store) ConsumeNonce(ctx context.Context, hash string, now time.Time) (bool, error) {
	ok := false
	err := s.Tx(ctx, func(tx *Store) error {
		var ea, ua sql.NullString
		err := tx.q.QueryRowContext(ctx, `SELECT expires_at, used_at FROM siwa_nonces WHERE nonce_hash = ?`, hash).Scan(&ea, &ua)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if ua.Valid {
			return nil
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE siwa_nonces SET used_at = ? WHERE nonce_hash = ?`, ts(now), hash); err != nil {
			return err
		}
		ok = now.Before(pt(ea))
		return nil
	})
	return ok, err
}

// PurgeNonces deletes nonces that expired before t.
func (s *Store) PurgeNonces(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM siwa_nonces WHERE expires_at < ?`, ts(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- lifecycle events ----

// InsertLifecycleEvent records a marker once per (household, kind, anchor).
func (s *Store) InsertLifecycleEvent(ctx context.Context, householdID, kind, anchor string, due time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx, `INSERT OR IGNORE INTO lifecycle_events (household_id, kind, anchor, due_at, created_at) VALUES (?,?,?,?,?)`,
		householdID, kind, anchor, ts(due), ts(s.Now()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) LifecycleEvents(ctx context.Context, householdID string) ([]LifecycleEvent, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, household_id, kind, anchor, due_at, created_at FROM lifecycle_events WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LifecycleEvent
	for rows.Next() {
		var e LifecycleEvent
		var d, c sql.NullString
		if err := rows.Scan(&e.ID, &e.HouseholdID, &e.Kind, &e.Anchor, &d, &c); err != nil {
			return nil, err
		}
		e.DueAt, e.CreatedAt = pt(d), pt(c)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- pending Apple revocations ----

func (s *Store) AddPendingRevocation(ctx context.Context, accountID string, tokenEnc []byte) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO pending_apple_revocations (account_id, token_enc, created_at) VALUES (?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET token_enc = excluded.token_enc, done_at = NULL`, accountID, tokenEnc, ts(s.Now()))
	return err
}

func (s *Store) PendingRevocations(ctx context.Context) ([]PendingRevocation, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT account_id, token_enc, created_at, attempts, last_error FROM pending_apple_revocations WHERE done_at IS NULL ORDER BY created_at, account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingRevocation
	for rows.Next() {
		var p PendingRevocation
		var c sql.NullString
		if err := rows.Scan(&p.AccountID, &p.TokenEnc, &c, &p.Attempts, &p.LastError); err != nil {
			return nil, err
		}
		p.CreatedAt = pt(c)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) FinishRevocation(ctx context.Context, accountID string, errMsg string) error {
	if errMsg == "" {
		return s.execOne(ctx, `UPDATE pending_apple_revocations SET done_at = ?, token_enc = x'', attempts = attempts + 1 WHERE account_id = ?`, ts(s.Now()), accountID)
	}
	return s.execOne(ctx, `UPDATE pending_apple_revocations SET attempts = attempts + 1, last_error = ? WHERE account_id = ?`, errMsg, accountID)
}
