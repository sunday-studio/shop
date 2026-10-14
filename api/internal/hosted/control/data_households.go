package control

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const householdCols = `id, owner_account_id, name, status, created_at, tombstoned_at, deleted_at, deletion_requested_at, forced_lapse_at, plan_state_cache, read_only_since, delete_after, limits_override`

func scanHousehold(r scanner) (Household, error) {
	var h Household
	var ca, to, de, dr, fl, ro, da sql.NullString
	err := r.Scan(&h.ID, &h.OwnerAccountID, &h.Name, &h.Status, &ca, &to, &de, &dr, &fl, &h.PlanStateCache, &ro, &da, &h.LimitsOverride)
	if err != nil {
		return h, err
	}
	h.CreatedAt, h.TombstonedAt, h.DeletedAt, h.DeletionRequestedAt = pt(ca), pt(to), pt(de), pt(dr)
	h.ForcedLapseAt, h.ReadOnlySince, h.DeleteAfter = pt(fl), pt(ro), pt(da)
	return h, nil
}

func (s *Store) queryHouseholds(ctx context.Context, where string, args ...any) ([]Household, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+householdCols+` FROM households `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Household
	for rows.Next() {
		h, err := scanHousehold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) InsertHousehold(ctx context.Context, h Household) (Household, error) {
	if h.ID == "" {
		h.ID = NewUUID()
	}
	if h.Name == "" {
		h.Name = "Home"
	}
	if h.Status == "" {
		h.Status = HouseholdActive
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.Now()
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO households (id, owner_account_id, name, status, created_at) VALUES (?,?,?,?,?)`,
		h.ID, h.OwnerAccountID, h.Name, h.Status, ts(h.CreatedAt))
	return h, err
}

func (s *Store) HouseholdByID(ctx context.Context, id string) (Household, error) {
	hs, err := s.queryHouseholds(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Household{}, err
	}
	if len(hs) == 0 {
		return Household{}, ErrNotFound
	}
	return hs[0], nil
}

// HouseholdOfAccount returns the household the account is currently a member of.
func (s *Store) HouseholdOfAccount(ctx context.Context, accountID string) (Household, Member, error) {
	m, err := s.MemberOf(ctx, accountID)
	if err != nil {
		return Household{}, Member{}, err
	}
	h, err := s.HouseholdByID(ctx, m.HouseholdID)
	return h, m, err
}

// LatestOwnedHousehold returns the newest household the account owns (any status).
func (s *Store) LatestOwnedHousehold(ctx context.Context, accountID string) (Household, error) {
	hs, err := s.queryHouseholds(ctx, `WHERE owner_account_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, accountID)
	if err != nil {
		return Household{}, err
	}
	if len(hs) == 0 {
		return Household{}, ErrNotFound
	}
	return hs[0], nil
}

// ListHouseholds returns households with one of the statuses, oldest first.
func (s *Store) ListHouseholds(ctx context.Context, statuses ...string) ([]Household, error) {
	where := ``
	var args []any
	if len(statuses) > 0 {
		where = `WHERE status IN (?` + repeat(",?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	return s.queryHouseholds(ctx, where+` ORDER BY created_at, rowid`, args...)
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func (s *Store) SetHouseholdOwner(ctx context.Context, id, owner string) error {
	return s.execOne(ctx, `UPDATE households SET owner_account_id = ? WHERE id = ?`, owner, id)
}
func (s *Store) SetHouseholdDeletionRequested(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE households SET deletion_requested_at = ? WHERE id = ?`, nts(at), id)
}
func (s *Store) SetHouseholdForcedLapse(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE households SET forced_lapse_at = ? WHERE id = ?`, nts(at), id)
}
func (s *Store) SetHouseholdLimitsOverride(ctx context.Context, id, json string) error {
	return s.execOne(ctx, `UPDATE households SET limits_override = ? WHERE id = ?`, json, id)
}

// SetHouseholdPlanCache stores the last computed plan state and lapse dates.
func (s *Store) SetHouseholdPlanCache(ctx context.Context, id, state string, readOnlySince, deleteAfter time.Time) error {
	return s.execOne(ctx, `UPDATE households SET plan_state_cache = ?, read_only_since = ?, delete_after = ? WHERE id = ?`,
		state, nts(readOnlySince), nts(deleteAfter), id)
}

// TombstoneHousehold is stage 1 of deletion.
func (s *Store) TombstoneHousehold(ctx context.Context, id string, at time.Time) error {
	return s.execOne(ctx, `UPDATE households SET status = 'tombstoned', tombstoned_at = ? WHERE id = ? AND status = 'active'`, ts(at), id)
}

// UntombstoneHousehold undoes stage 1 (the owner came back before stage 2).
func (s *Store) UntombstoneHousehold(ctx context.Context, id string) error {
	return s.execOne(ctx, `UPDATE households SET status = 'active', tombstoned_at = NULL WHERE id = ? AND status = 'tombstoned'`, id)
}

// MarkHouseholdDeleted is stage 2: the files are gone. Memberships are removed.
func (s *Store) MarkHouseholdDeleted(ctx context.Context, id string, at time.Time) error {
	if err := s.execOne(ctx, `UPDATE households SET status = 'deleted', deleted_at = ?, plan_state_cache = 'deleted' WHERE id = ? AND status <> 'deleted'`, ts(at), id); err != nil {
		return err
	}
	_, err := s.q.ExecContext(ctx, `DELETE FROM memberships WHERE household_id = ?`, id)
	return err
}

// ---- members ----

func (s *Store) AddMember(ctx context.Context, m Member) error {
	if m.JoinedAt.IsZero() {
		m.JoinedAt = s.Now()
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO memberships (household_id, account_id, role, joined_at) VALUES (?,?,?,?)`,
		m.HouseholdID, m.AccountID, m.Role, ts(m.JoinedAt))
	return err
}

func (s *Store) RemoveMember(ctx context.Context, householdID, accountID string) error {
	return s.execOne(ctx, `DELETE FROM memberships WHERE household_id = ? AND account_id = ?`, householdID, accountID)
}

func (s *Store) SetMemberRole(ctx context.Context, householdID, accountID, role string) error {
	return s.execOne(ctx, `UPDATE memberships SET role = ? WHERE household_id = ? AND account_id = ?`, role, householdID, accountID)
}

func (s *Store) MemberOf(ctx context.Context, accountID string) (Member, error) {
	var m Member
	var ja sql.NullString
	err := s.q.QueryRowContext(ctx, `SELECT household_id, account_id, role, joined_at FROM memberships WHERE account_id = ?`, accountID).
		Scan(&m.HouseholdID, &m.AccountID, &m.Role, &ja)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNoHousehold
	}
	m.JoinedAt = pt(ja)
	return m, err
}

// Members lists a household's members, owner first.
func (s *Store) Members(ctx context.Context, householdID string) ([]Member, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT m.household_id, m.account_id, m.role, m.joined_at, COALESCE(a.display_name,'')
		FROM memberships m JOIN accounts a ON a.id = m.account_id WHERE m.household_id = ?
		ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, m.joined_at, m.account_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var ja sql.NullString
		if err := rows.Scan(&m.HouseholdID, &m.AccountID, &m.Role, &ja, &m.DisplayName); err != nil {
			return nil, err
		}
		m.JoinedAt = pt(ja)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) CountMembers(ctx context.Context, householdID string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE household_id = ?`, householdID).Scan(&n)
	return n, err
}

// ---- invites ----

func (s *Store) InsertInvite(ctx context.Context, i Invite) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO invites (id, household_id, code_hash, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		i.ID, i.HouseholdID, i.CodeHash, i.CreatedBy, ts(i.CreatedAt), ts(i.ExpiresAt))
	return err
}

func scanInvite(r scanner) (Invite, error) {
	var i Invite
	var ca, ea, ub, ua, ra sql.NullString
	err := r.Scan(&i.ID, &i.HouseholdID, &i.CodeHash, &i.CreatedBy, &ca, &ea, &ub, &ua, &ra)
	i.CreatedAt, i.ExpiresAt, i.UsedBy, i.UsedAt, i.RevokedAt = pt(ca), pt(ea), ub.String, pt(ua), pt(ra)
	return i, err
}

const inviteCols = `id, household_id, code_hash, created_by, created_at, expires_at, used_by, used_at, revoked_at`

func (s *Store) InviteByHash(ctx context.Context, hash string) (Invite, error) {
	i, err := scanInvite(s.q.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites WHERE code_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrInviteInvalid
	}
	return i, err
}

func (s *Store) InviteByID(ctx context.Context, id string) (Invite, error) {
	i, err := scanInvite(s.q.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

// OpenInvites lists unused, unrevoked, unexpired invites.
func (s *Store) OpenInvites(ctx context.Context, householdID string, now time.Time) ([]Invite, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+inviteCols+` FROM invites WHERE household_id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ? ORDER BY created_at, id`,
		householdID, ts(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// MarkInviteUsed marks the invite used exactly once; it reports false if someone else won.
func (s *Store) MarkInviteUsed(ctx context.Context, id, by string, now time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE invites SET used_by = ?, used_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`,
		by, ts(now), id, ts(now))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) RevokeInvite(ctx context.Context, id, householdID string, now time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE invites SET revoked_at = ? WHERE id = ? AND household_id = ? AND used_at IS NULL AND revoked_at IS NULL`,
		ts(now), id, householdID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RevokeHouseholdInvites revokes every open invite of a household.
func (s *Store) RevokeHouseholdInvites(ctx context.Context, householdID string, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE invites SET revoked_at = ? WHERE household_id = ? AND used_at IS NULL AND revoked_at IS NULL`, ts(now), householdID)
	return err
}

// ---- devices ----

const deviceCols = `id, account_id, token_hash, name, kind, created_at, last_used_at, revoked_at`

func scanDevice(r scanner) (Device, error) {
	var d Device
	var ca, lu, ra sql.NullString
	err := r.Scan(&d.ID, &d.AccountID, &d.TokenHash, &d.Name, &d.Kind, &ca, &lu, &ra)
	d.CreatedAt, d.LastUsedAt, d.RevokedAt = pt(ca), pt(lu), pt(ra)
	return d, err
}

func (s *Store) InsertDevice(ctx context.Context, d Device) (Device, error) {
	if d.Kind == "" {
		d.Kind = "device"
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = s.Now()
	}
	res, err := s.q.ExecContext(ctx, `INSERT INTO devices (account_id, token_hash, name, kind, created_at) VALUES (?,?,?,?,?)`,
		d.AccountID, d.TokenHash, d.Name, d.Kind, ts(d.CreatedAt))
	if err != nil {
		return d, err
	}
	d.ID, _ = res.LastInsertId()
	return d, nil
}

// DeviceByTokenHash returns the device even when revoked (caller checks RevokedAt).
func (s *Store) DeviceByTokenHash(ctx context.Context, hash string) (Device, error) {
	d, err := scanDevice(s.q.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Store) DeviceByID(ctx context.Context, id int64) (Device, error) {
	d, err := scanDevice(s.q.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// ListDevices returns the account's devices that are not revoked, oldest first.
func (s *Store) ListDevices(ctx context.Context, accountID string) ([]Device, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE account_id = ? AND revoked_at IS NULL ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) TouchDevice(ctx context.Context, id int64, at time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE devices SET last_used_at = ? WHERE id = ?`, ts(at), id)
	return err
}

// RevokeDevice revokes one device of the account (false if not found or already revoked).
func (s *Store) RevokeDevice(ctx context.Context, id int64, accountID string, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE devices SET revoked_at = ? WHERE id = ? AND account_id = ? AND revoked_at IS NULL`, ts(at), id, accountID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RevokeAccountDevices revokes all devices of the account except exceptID (0 = none kept).
func (s *Store) RevokeAccountDevices(ctx context.Context, accountID string, exceptID int64, at time.Time) (int, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE devices SET revoked_at = ? WHERE account_id = ? AND id <> ? AND revoked_at IS NULL`, ts(at), accountID, exceptID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
