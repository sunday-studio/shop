// Package control is the hosted control database: accounts, households, members, invites,
// devices, entitlements, grants, usage counters, the audit log and idempotency tables. It is
// its own SQLite file with its own migrations, separate from every household database.
//
// Store is the data layer (small methods, no policy). Service (service.go) holds the
// invariants: household cap, one owner, single-use invites, transfer rules, two-stage
// account deletion.
package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"shop/internal/hosted/hclock"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type dbtx interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// Store is the control database handle. A Store inside Tx is bound to one transaction.
type Store struct {
	db    *sql.DB
	q     dbtx
	inTx  bool
	clock hclock.Clock
}

// Open opens (creating) the control database at path and applies migrations.
func Open(path string, clock hclock.Clock) (*Store, error) {
	if clock == nil {
		clock = hclock.System{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open control db %s: %w", path, err)
	}
	s := &Store{db: db, q: db, clock: clock}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Now is the store's clock reading.
func (s *Store) Now() time.Time { return s.clock.Now().UTC().Truncate(time.Second) }

// Clock exposes the clock the store was opened with.
func (s *Store) Clock() hclock.Clock { return s.clock }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, n).Scan(&one)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrationFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, n, ts(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Tx runs fn in one transaction. Nested calls join the outer transaction.
func (s *Store) Tx(ctx context.Context, fn func(tx *Store) error) error {
	if s.inTx {
		return fn(s)
	}
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ts := &Store{db: s.db, q: t, inTx: true, clock: s.clock}
	if err := fn(ts); err != nil {
		t.Rollback()
		return err
	}
	return t.Commit()
}

// ---- time and id helpers ----

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func nts(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return ts(t)
}

func pt(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v.String)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FormatTime renders a time the way the contract does (RFC 3339 UTC, seconds).
func FormatTime(t time.Time) string { return ts(t) }

// NewUUID returns a random version 4 UUID, lowercase.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// RandomCode returns n Crockford base32 characters (no I L O U).
func RandomCode(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, n)
	for i := range b {
		out[i] = crockford[int(b[i])%32]
	}
	return string(out)
}

// GroupCode inserts a dash every 4 characters.
func GroupCode(c string) string {
	var sb strings.Builder
	for i, r := range c {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// NormalizeCode upper-cases, drops dashes and spaces and applies the Crockford look-alike rules.
func NormalizeCode(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	var sb strings.Builder
	for _, r := range c {
		switch r {
		case '-', ' ':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// HashSecret is the hash stored for invite codes and device tokens.
func HashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// HashCode hashes an invite or support code after normalisation.
func HashCode(c string) string { return HashSecret(NormalizeCode(c)) }

// NewToken returns a device token "st_..." (32 random bytes, base64url) and its hash.
func NewToken() (token, hash string) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	token = "st_" + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashSecret(token)
}
