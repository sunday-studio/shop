package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const accountCols = `id, apple_sub, display_name, email, email_is_private, email_deliverable, app_account_token, support_code, status, apple_refresh_token_enc, created_at, last_login_at, deletion_requested_at, deleted_at`

type scanner interface{ Scan(...any) error }

func scanAccount(r scanner) (Account, error) {
	var a Account
	var dn, em, ca, ll, dr, da sql.NullString
	var eip, ed int
	err := r.Scan(&a.ID, &a.AppleSub, &dn, &em, &eip, &ed, &a.AppAccountToken, &a.SupportCode, &a.Status, &a.RefreshTokenEnc, &ca, &ll, &dr, &da)
	if err != nil {
		return a, err
	}
	a.DisplayName, a.Email = dn.String, em.String
	a.EmailIsPrivate, a.EmailDeliverable = eip == 1, ed == 1
	a.CreatedAt, a.LastLoginAt, a.DeletionRequestedAt, a.DeletedAt = pt(ca), pt(ll), pt(dr), pt(da)
	return a, nil
}

func (s *Store) oneAccount(ctx context.Context, where string, args ...any) (Account, error) {
	a, err := scanAccount(s.q.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// InsertAccount creates an account row. ID, tokens and codes are generated when empty.
func (s *Store) InsertAccount(ctx context.Context, a Account) (Account, error) {
	if a.ID == "" {
		a.ID = NewUUID()
	}
	if a.AppAccountToken == "" {
		a.AppAccountToken = NewUUID()
	}
	if a.SupportCode == "" {
		a.SupportCode = GroupCode(RandomCode(8))
	}
	if a.Status == "" {
		a.Status = AccountActive
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = s.Now()
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO accounts (`+accountCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.AppleSub, nullStr(a.DisplayName), nullStr(a.Email), b2i(a.EmailIsPrivate), b2i(a.EmailDeliverable),
		a.AppAccountToken, a.SupportCode, a.Status, a.RefreshTokenEnc, ts(a.CreatedAt), nts(a.LastLoginAt), nts(a.DeletionRequestedAt), nts(a.DeletedAt))
	return a, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) AccountByID(ctx context.Context, id string) (Account, error) {
	return s.oneAccount(ctx, `id = ?`, id)
}
func (s *Store) AccountBySub(ctx context.Context, sub string) (Account, error) {
	return s.oneAccount(ctx, `apple_sub = ?`, sub)
}
func (s *Store) AccountByAppToken(ctx context.Context, token string) (Account, error) {
	return s.oneAccount(ctx, `app_account_token = ?`, strings.ToLower(token))
}
func (s *Store) AccountBySupportCode(ctx context.Context, code string) (Account, error) {
	return s.oneAccount(ctx, `support_code = ?`, GroupCode(NormalizeCode(code)))
}

// ListAccountsDeletionDue returns accounts whose deletion was requested at or before t.
func (s *Store) ListAccountsDeletionRequested(ctx context.Context) ([]Account, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE deletion_requested_at IS NOT NULL AND status <> 'deleted' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.q.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetAccountLogin(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE accounts SET last_login_at = ? WHERE id = ?`, ts(at), id)
}
func (s *Store) SetAccountDisplayName(ctx context.Context, id, name string) error {
	return s.execOne(ctx, `UPDATE accounts SET display_name = ? WHERE id = ?`, nullStr(name), id)
}
func (s *Store) SetAccountStatus(ctx context.Context, id, status string) error {
	return s.execOne(ctx, `UPDATE accounts SET status = ? WHERE id = ?`, status, id)
}
func (s *Store) SetAccountEmail(ctx context.Context, id, email string, private bool) error {
	return s.execOne(ctx, `UPDATE accounts SET email = ?, email_is_private = ? WHERE id = ?`, nullStr(email), b2i(private), id)
}
func (s *Store) SetEmailDeliverable(ctx context.Context, id string, ok bool) error {
	return s.execOne(ctx, `UPDATE accounts SET email_deliverable = ? WHERE id = ?`, b2i(ok), id)
}
func (s *Store) SetRefreshToken(ctx context.Context, id string, enc []byte) error {
	return s.execOne(ctx, `UPDATE accounts SET apple_refresh_token_enc = ? WHERE id = ?`, enc, id)
}
func (s *Store) SetAccountDeletionRequested(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE accounts SET deletion_requested_at = ? WHERE id = ?`, nts(at), id)
}

// EraseAccount removes the personal fields and keeps the row (billing ledger link, token).
func (s *Store) EraseAccount(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE accounts SET status = 'deleted', deleted_at = ?, display_name = NULL, email = NULL,
		apple_refresh_token_enc = NULL, apple_sub = ?, email_is_private = 0, email_deliverable = 0 WHERE id = ?`,
		ts(at), fmt.Sprintf("deleted:%s", id), id)
}
