package control

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---- idempotency keys ----

// IdemRecord is one stored idempotent request.
type IdemRecord struct {
	Scope, Key, Method, Path, BodyHash string
	Status                             int
	ContentType, Location              string
	Response                           []byte
	CreatedAt                          time.Time
	Done                               bool
}

// Idempotency begin results.
const (
	IdemNew        = "new"         // this request owns the key now
	IdemReplay     = "replay"      // a finished request: answer with Record
	IdemMismatch   = "mismatch"    // same key, other method, path or body
	IdemInProgress = "in_progress" // the first request is still running
)

// IdemStaleAfter is how long an unfinished claim blocks a retry (a crashed request frees its key).
const IdemStaleAfter = 2 * time.Minute

// BeginIdempotent claims (scope, key) for a request, or says what already holds it. Records older
// than ttl are forgotten first.
func (s *Store) BeginIdempotent(ctx context.Context, scope, key, method, path, bodyHash string, ttl time.Duration) (string, IdemRecord, error) {
	var out IdemRecord
	result := IdemNew
	err := s.Tx(ctx, func(tx *Store) error {
		now := tx.Now()
		var rec IdemRecord
		var ca, done sql.NullString
		var resp []byte
		err := tx.q.QueryRowContext(ctx, `SELECT method, path, body_hash, status, content_type, location, response, created_at, completed_at FROM idempotency_keys WHERE scope = ? AND key = ?`,
			scope, key).Scan(&rec.Method, &rec.Path, &rec.BodyHash, &rec.Status, &rec.ContentType, &rec.Location, &resp, &ca, &done)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			rec.Scope, rec.Key, rec.Response, rec.CreatedAt, rec.Done = scope, key, resp, pt(ca), done.Valid
			age := now.Sub(rec.CreatedAt)
			stale := (rec.Done && age >= ttl) || (!rec.Done && age >= IdemStaleAfter)
			if !stale {
				out = rec
				switch {
				case rec.Method != method || rec.Path != path || rec.BodyHash != bodyHash:
					result = IdemMismatch
				case rec.Done:
					result = IdemReplay
				default:
					result = IdemInProgress
				}
				return nil
			}
			if _, err := tx.q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE scope = ? AND key = ?`, scope, key); err != nil {
				return err
			}
		}
		_, err = tx.q.ExecContext(ctx, `INSERT INTO idempotency_keys (scope, key, method, path, body_hash, created_at) VALUES (?,?,?,?,?,?)`,
			scope, key, method, path, bodyHash, ts(now))
		return err
	})
	return result, out, err
}

// FinishIdempotent stores the response of the request that owns the key.
func (s *Store) FinishIdempotent(ctx context.Context, scope, key string, status int, contentType, location string, body []byte) error {
	_, err := s.q.ExecContext(ctx, `UPDATE idempotency_keys SET status = ?, content_type = ?, location = ?, response = ?, completed_at = ? WHERE scope = ? AND key = ?`,
		status, contentType, location, body, ts(s.Now()), scope, key)
	return err
}

// ForgetIdempotent drops a claim (the request failed in a way that must be retried for real).
func (s *Store) ForgetIdempotent(ctx context.Context, scope, key string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE scope = ? AND key = ?`, scope, key)
	return err
}

// PurgeIdempotent deletes records created before t.
func (s *Store) PurgeIdempotent(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < ?`, ts(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- export jobs ----

// Export job states.
const (
	ExportQueued  = "queued"
	ExportRunning = "running"
	ExportReady   = "ready"
	ExportFailed  = "failed"
	ExportExpired = "expired"
)

type ExportJob struct {
	ID          string
	HouseholdID string
	RequestedBy string
	Status      string
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	ExpiresAt   time.Time
	SizeBytes   int64
	Path        string
	Error       string
}

const exportCols = `id, household_id, requested_by, status, created_at, started_at, finished_at, expires_at, size_bytes, path, error`

func scanExport(r scanner) (ExportJob, error) {
	var j ExportJob
	var ca, sa, fa, ea sql.NullString
	var size sql.NullInt64
	err := r.Scan(&j.ID, &j.HouseholdID, &j.RequestedBy, &j.Status, &ca, &sa, &fa, &ea, &size, &j.Path, &j.Error)
	j.CreatedAt, j.StartedAt, j.FinishedAt, j.ExpiresAt, j.SizeBytes = pt(ca), pt(sa), pt(fa), pt(ea), size.Int64
	return j, err
}

func (s *Store) InsertExportJob(ctx context.Context, householdID, requestedBy string) (ExportJob, error) {
	j := ExportJob{ID: NewUUID(), HouseholdID: householdID, RequestedBy: requestedBy, Status: ExportQueued, CreatedAt: s.Now()}
	_, err := s.q.ExecContext(ctx, `INSERT INTO export_jobs (id, household_id, requested_by, status, created_at) VALUES (?,?,?,?,?)`,
		j.ID, householdID, requestedBy, j.Status, ts(j.CreatedAt))
	return j, err
}

func (s *Store) ExportJobByID(ctx context.Context, id string) (ExportJob, error) {
	j, err := scanExport(s.q.QueryRowContext(ctx, `SELECT `+exportCols+` FROM export_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// LatestExportJob is the household's most recent job.
func (s *Store) LatestExportJob(ctx context.Context, householdID string) (ExportJob, error) {
	j, err := scanExport(s.q.QueryRowContext(ctx, `SELECT `+exportCols+` FROM export_jobs WHERE household_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, householdID))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// LatestExportJobBy is the most recent job the account asked for in the household.
func (s *Store) LatestExportJobBy(ctx context.Context, householdID, accountID string) (ExportJob, error) {
	j, err := scanExport(s.q.QueryRowContext(ctx, `SELECT `+exportCols+` FROM export_jobs WHERE household_id = ? AND requested_by = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, householdID, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// ExportJobsByStatus lists jobs in any of the states, oldest first.
func (s *Store) ExportJobsByStatus(ctx context.Context, statuses ...string) ([]ExportJob, error) {
	q := `SELECT ` + exportCols + ` FROM export_jobs WHERE status IN (?` + repeat(",?", len(statuses)-1) + `) ORDER BY created_at, rowid`
	args := make([]any, len(statuses))
	for i, st := range statuses {
		args[i] = st
	}
	rows, err := s.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportJob
	for rows.Next() {
		j, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ExportJobsOfHousehold lists a household's jobs, newest first.
func (s *Store) ExportJobsOfHousehold(ctx context.Context, householdID string) ([]ExportJob, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+exportCols+` FROM export_jobs WHERE household_id = ? ORDER BY created_at DESC, rowid DESC`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportJob
	for rows.Next() {
		j, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ClaimExportJob moves a queued job to running (false if someone else took it).
func (s *Store) ClaimExportJob(ctx context.Context, id string) (bool, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE export_jobs SET status = 'running', started_at = ? WHERE id = ? AND status = 'queued'`, ts(s.Now()), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RequeueRunningExports puts jobs interrupted by a restart back in the queue.
func (s *Store) RequeueRunningExports(ctx context.Context) (int64, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE export_jobs SET status = 'queued', started_at = NULL WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) FinishExportJob(ctx context.Context, id, path string, size int64, expires time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE export_jobs SET status = 'ready', finished_at = ?, expires_at = ?, size_bytes = ?, path = ?, error = '' WHERE id = ?`,
		ts(s.Now()), ts(expires), size, path, id)
	return err
}

func (s *Store) FailExportJob(ctx context.Context, id, msg string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE export_jobs SET status = 'failed', finished_at = ?, error = ? WHERE id = ?`, ts(s.Now()), msg, id)
	return err
}

func (s *Store) ExpireExportJob(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE export_jobs SET status = 'expired', path = '' WHERE id = ?`, id)
	return err
}

// CountExportsSince is the number of jobs a household created since t.
func (s *Store) CountExportsSince(ctx context.Context, householdID string, since time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM export_jobs WHERE household_id = ? AND created_at >= ?`, householdID, ts(since)).Scan(&n)
	return n, err
}

// ---- scheduler lock ----

// TryLock takes the named lock for ttl if nobody holds it (or the holder's lease ran out).
func (s *Store) TryLock(ctx context.Context, name, holder string, ttl time.Duration) (bool, error) {
	got := false
	err := s.Tx(ctx, func(tx *Store) error {
		now := tx.Now()
		var h string
		var exp sql.NullString
		err := tx.q.QueryRowContext(ctx, `SELECT holder, expires_at FROM job_locks WHERE name = ?`, name).Scan(&h, &exp)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case h != holder && now.Before(pt(exp)):
			return nil
		}
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO job_locks (name, holder, expires_at) VALUES (?,?,?) ON CONFLICT(name) DO UPDATE SET holder = excluded.holder, expires_at = excluded.expires_at`,
			name, holder, ts(now.Add(ttl))); err != nil {
			return err
		}
		got = true
		return nil
	})
	return got, err
}

// Unlock releases the lock if holder still has it.
func (s *Store) Unlock(ctx context.Context, name, holder string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM job_locks WHERE name = ? AND holder = ?`, name, holder)
	return err
}

// HouseholdOwnerName is the owner's display name ("" when unset), for the public invite preview.
func (s *Store) HouseholdOwnerName(ctx context.Context, householdID string) (string, error) {
	h, err := s.HouseholdByID(ctx, householdID)
	if err != nil {
		return "", err
	}
	a, err := s.AccountByID(ctx, h.OwnerAccountID)
	if err != nil {
		return "", err
	}
	return a.DisplayName, nil
}
