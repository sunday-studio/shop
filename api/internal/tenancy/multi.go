package tenancy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// MultiConfig configures a Multi provider.
type MultiConfig struct {
	// Root is the data directory. Household h lives in Root/households/<h>/ (shop.db, images/).
	Root string
	// BackupRoot, when set, is where per-household backups go: BackupRoot/households/<h>/. Erasing a
	// household removes that folder too.
	BackupRoot string
	// MaxOpen bounds the open databases (default 64). Households in use are never evicted.
	MaxOpen int
	Log     *slog.Logger
}

// Multi opens one SQLite file per household, lazily.
type Multi struct {
	cfg MultiConfig

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
}

type entry struct {
	id       string
	ready    chan struct{} // closed when opening finished
	t        *Tenant
	err      error
	refs     int
	lastUsed time.Time
	erasing  bool
	idle     chan struct{} // closed-and-replaced when refs drops to 0 (waited on by Erase)
}

// NewMulti returns a provider; nothing is opened until a household is acquired.
func NewMulti(cfg MultiConfig) (*Multi, error) {
	if cfg.Root == "" {
		return nil, errors.New("tenancy: MultiConfig.Root is required")
	}
	if cfg.MaxOpen <= 0 {
		cfg.MaxOpen = 64
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Root, "households"), 0o755); err != nil {
		return nil, err
	}
	return &Multi{cfg: cfg, entries: map[string]*entry{}}, nil
}

func (m *Multi) Mode() Mode { return ModeMulti }

// Dir is the household's folder (it need not exist).
func (m *Multi) Dir(id string) string { return filepath.Join(m.cfg.Root, "households", id) }

// BackupDir is where the household's backups go ("" when BackupRoot is not set).
func (m *Multi) BackupDir(id string) string {
	if m.cfg.BackupRoot == "" {
		return ""
	}
	return filepath.Join(m.cfg.BackupRoot, "households", id)
}

func (m *Multi) dbPath(id string) string { return filepath.Join(m.Dir(id), "shop.db") }

// Provision creates the household's folder and database (idempotent) and returns once its schema is current.
func (m *Multi) Provision(ctx context.Context, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("tenancy: invalid household id %q", id)
	}
	if err := os.MkdirAll(filepath.Join(m.Dir(id), "images"), 0o755); err != nil {
		return err
	}
	_, release, err := m.Acquire(ctx, id)
	if err != nil {
		return err
	}
	release()
	return nil
}

// Exists reports whether the household has a database file.
func (m *Multi) Exists(id string) bool {
	if !ValidID(id) {
		return false
	}
	_, err := os.Stat(m.dbPath(id))
	return err == nil
}

func (m *Multi) Acquire(ctx context.Context, id string) (*Tenant, func(), error) {
	if !ValidID(id) {
		return nil, nil, ErrUnknownTenant
	}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, nil, errors.New("tenancy: provider closed")
		}
		e := m.entries[id]
		if e == nil {
			// Open it. Folder exists only if the household was provisioned (or a database file is there).
			if _, err := os.Stat(m.Dir(id)); err != nil {
				m.mu.Unlock()
				return nil, nil, ErrUnknownTenant
			}
			e = &entry{id: id, ready: make(chan struct{}), idle: make(chan struct{})}
			m.entries[id] = e
			m.mu.Unlock()
			m.open(ctx, e)
			continue // re-enter to take the reference under the lock
		}
		select {
		case <-e.ready:
		default:
			m.mu.Unlock()
			select {
			case <-e.ready:
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			continue
		}
		if e.erasing {
			m.mu.Unlock()
			return nil, nil, ErrUnknownTenant
		}
		if e.err != nil || e.t == nil {
			err := e.err
			delete(m.entries, id) // a failed open is retried on the next request
			m.mu.Unlock()
			return nil, nil, &UnavailableError{ID: id, Err: err}
		}
		e.refs++
		e.lastUsed = time.Now()
		t := e.t
		toClose := m.evictLocked(id)
		m.mu.Unlock()
		for _, c := range toClose {
			if err := c.close(); err != nil {
				m.cfg.Log.Warn("close household", "id", c.ID, "err", err)
			}
		}
		var once sync.Once
		return t, func() { once.Do(func() { m.release(e) }) }, nil
	}
}

func (m *Multi) open(ctx context.Context, e *entry) {
	dir := m.Dir(e.id)
	db, err := OpenDB(ctx, m.dbPath(e.id), dir, m.cfg.Log)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.cfg.Log.Error("open household", "id", e.id, "err", err)
		e.err = err
	} else {
		e.t = &Tenant{ID: e.id, DB: db, Dir: dir, ImagesDir: filepath.Join(dir, "images")}
		_ = os.MkdirAll(e.t.ImagesDir, 0o755)
		e.lastUsed = time.Now()
	}
	close(e.ready)
}

func (m *Multi) release(e *entry) {
	m.mu.Lock()
	e.refs--
	if e.refs == 0 {
		close(e.idle)
		e.idle = make(chan struct{})
	}
	toClose := m.evictLocked("")
	m.mu.Unlock()
	for _, c := range toClose {
		if err := c.close(); err != nil {
			m.cfg.Log.Warn("close household", "id", c.ID, "err", err)
		}
	}
}

// evictLocked removes least recently used idle tenants beyond MaxOpen (never keep) and returns them to be closed.
func (m *Multi) evictLocked(keep string) []*Tenant {
	var open []*entry
	for _, e := range m.entries {
		if e.t != nil {
			open = append(open, e)
		}
	}
	if len(open) <= m.cfg.MaxOpen {
		return nil
	}
	sort.Slice(open, func(i, j int) bool { return open[i].lastUsed.Before(open[j].lastUsed) })
	var out []*Tenant
	for _, e := range open {
		if len(open)-len(out) <= m.cfg.MaxOpen {
			break
		}
		if e.refs > 0 || e.id == keep || e.erasing {
			continue
		}
		out = append(out, e.t)
		delete(m.entries, e.id)
	}
	return out
}

// Open reports how many databases are open now.
func (m *Multi) Open() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.entries {
		if e.t != nil {
			n++
		}
	}
	return n
}

func (m *Multi) IDs(context.Context) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(m.cfg.Root, "households"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range ents {
		if d.IsDir() && ValidID(d.Name()) && m.Exists(d.Name()) {
			ids = append(ids, d.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *Multi) Erase(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrUnknownTenant
	}
	m.mu.Lock()
	e := m.entries[id]
	for e != nil {
		select {
		case <-e.ready:
		default: // an open is in flight: wait for it, then erase
			m.mu.Unlock()
			select {
			case <-e.ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			m.mu.Lock()
			e = m.entries[id]
			continue
		}
		break
	}
	if e == nil {
		// Not open: a placeholder keeps new users out while the files go.
		e = &entry{id: id, ready: make(chan struct{}), idle: make(chan struct{})}
		close(e.ready)
		m.entries[id] = e
	}
	e.erasing = true // new users get ErrUnknownTenant from here on
	for e.refs > 0 {
		idle := e.idle
		m.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			m.mu.Lock()
			e.erasing = false
			if e.t == nil {
				delete(m.entries, id)
			}
			m.mu.Unlock()
			return ctx.Err()
		}
		m.mu.Lock()
	}
	m.mu.Unlock()
	var err error
	if e.t != nil {
		if cerr := e.t.close(); cerr != nil {
			m.cfg.Log.Warn("close household before erase", "id", id, "err", cerr)
		}
	}
	err = os.RemoveAll(m.Dir(id))
	if err == nil {
		if b := m.BackupDir(id); b != "" {
			err = os.RemoveAll(b)
		}
	}
	m.mu.Lock()
	delete(m.entries, id)
	m.mu.Unlock()
	return err
}

func (m *Multi) Close() error {
	m.mu.Lock()
	m.closed = true
	var ts []*Tenant
	for id, e := range m.entries {
		if e.t != nil {
			ts = append(ts, e.t)
		}
		delete(m.entries, id)
	}
	m.mu.Unlock()
	var first error
	for _, t := range ts {
		if err := t.close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
