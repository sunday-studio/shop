// Package tenancy is the seam between "one server" and "many households".
//
// A Tenant is one household: its own SQLite database, its own image folder, and whatever the
// application attaches to it (the service graph, built once per open tenant). Every request resolves
// a Tenant from its bearer token through a Resolver; every service call then runs on that tenant's
// own database handle, so there is no handle through which another household's rows can be reached.
//
// Two providers exist:
//
//   - Single wraps today's setup: one database, one tenant (DefaultID). This is the default mode and
//     behaves exactly as before.
//   - Multi opens one SQLite file per household lazily (migrations on open, a small LRU of open
//     handles, safe close, erasure of a whole household).
//
// Which household a token belongs to is the Directory's business. The public repository ships only
// an in-memory Directory (tests and embedding); a hosted deployment supplies one backed by its
// control database. Nothing in this package knows about accounts, billing or sign-in.
package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// DefaultID is the id of the only tenant in single mode.
const DefaultID = "default"

var (
	// ErrUnknownToken: the token belongs to no household (the request is answered 401).
	ErrUnknownToken = errors.New("tenancy: unknown token")
	// ErrUnknownTenant: no such household (or it was erased).
	ErrUnknownTenant = errors.New("tenancy: unknown household")
	// ErrUnavailable: the household exists but its database could not be opened or migrated. Other
	// households are not affected. Use errors.As to get the id.
	ErrUnavailable = errors.New("tenancy: household unavailable")
	// ErrNotSupported: the provider cannot do this (erasing the only household of single mode).
	ErrNotSupported = errors.New("tenancy: not supported by this provider")
)

// UnavailableError wraps the reason a household could not be opened.
type UnavailableError struct {
	ID  string
	Err error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("household %s unavailable: %v", e.ID, e.Err)
}
func (e *UnavailableError) Unwrap() []error { return []error{ErrUnavailable, e.Err} }

// Mode names how a provider serves households.
type Mode string

const (
	ModeSingle Mode = "single"
	ModeMulti  Mode = "multi"
)

// Tenant is one open household. It is safe for concurrent use.
type Tenant struct {
	ID string
	// DB is the household's own database. No other household's data is reachable through it.
	DB *sql.DB
	// Dir is the household's folder: shop.db, images/, snapshots.
	Dir string
	// ImagesDir holds receipt and product images (and the logo cache).
	ImagesDir string

	buildMu sync.Mutex // serialises State builds (a build may call OnClose)
	mu      sync.Mutex // guards state and closers
	state   map[any]any
	closers []func()
}

// State returns the value attached under key, building it once with build. The application uses it
// to hang the per-household service graph on the tenant; it lives and dies with the open tenant.
func (t *Tenant) State(key any, build func() (any, error)) (any, error) {
	t.buildMu.Lock()
	defer t.buildMu.Unlock()
	t.mu.Lock()
	v, ok := t.state[key]
	t.mu.Unlock()
	if ok {
		return v, nil
	}
	v, err := build()
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.state == nil {
		t.state = map[any]any{}
	}
	t.state[key] = v
	t.mu.Unlock()
	return v, nil
}

// OnClose registers f to run (last registered first) when the tenant is closed, before its
// database is closed.
func (t *Tenant) OnClose(f func()) {
	t.mu.Lock()
	t.closers = append(t.closers, f)
	t.mu.Unlock()
}

func (t *Tenant) close() error {
	t.mu.Lock()
	cl := t.closers
	t.closers, t.state = nil, nil
	t.mu.Unlock()
	for i := len(cl) - 1; i >= 0; i-- {
		cl[i]()
	}
	if t.DB == nil {
		return nil
	}
	return t.DB.Close()
}

// Provider hands out tenants.
type Provider interface {
	Mode() Mode
	// Acquire returns the tenant and a release function. The tenant stays open (is not evicted or
	// closed) until released; always call release.
	Acquire(ctx context.Context, id string) (*Tenant, func(), error)
	// IDs lists the households that exist (open or not).
	IDs(ctx context.Context) ([]string, error)
	// Erase removes the household completely: database, images, and backup copies the provider
	// manages. It waits for in-flight users of the tenant and refuses new ones.
	Erase(ctx context.Context, id string) error
	// Close closes every open tenant.
	Close() error
}

// Resolver maps a bearer token to the household it belongs to.
type Resolver interface {
	Resolve(ctx context.Context, bearer string) (tenantID string, err error)
}

// Directory is the control seam: which household a token belongs to. A hosted deployment backs it
// with its control database. Token hashes are the same hashes the household database stores
// (auth.Hash), so no plain token ever reaches the directory.
type Directory interface {
	HouseholdForToken(ctx context.Context, tokenHash string) (householdID string, err error)
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidID reports whether id is usable as a household id and folder name (lower case letters,
// digits, dashes; no dots or slashes, so a folder name can never escape the data directory).
func ValidID(id string) bool { return idRe.MatchString(id) }
