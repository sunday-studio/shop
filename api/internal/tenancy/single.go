package tenancy

import (
	"context"
	"database/sql"
	"sync"
)

// Single is the default provider: the one database of a self-hosted server is the one tenant.
type Single struct {
	t    *Tenant
	once sync.Once
	err  error
}

// NewSingle wraps an open, migrated database. dir is the data directory (it holds shop.db and
// images/), imagesDir the image folder.
func NewSingle(db *sql.DB, dir, imagesDir string) *Single {
	return &Single{t: &Tenant{ID: DefaultID, DB: db, Dir: dir, ImagesDir: imagesDir}}
}

func (s *Single) Mode() Mode { return ModeSingle }

func (s *Single) Acquire(_ context.Context, id string) (*Tenant, func(), error) {
	if id != DefaultID {
		return nil, nil, ErrUnknownTenant
	}
	return s.t, func() {}, nil
}

func (s *Single) IDs(context.Context) ([]string, error) { return []string{DefaultID}, nil }

// Erase is not supported: the data directory of a self-hosted server is the owner's to delete.
func (s *Single) Erase(context.Context, string) error { return ErrNotSupported }

func (s *Single) Close() error {
	s.once.Do(func() { s.err = s.t.close() })
	return s.err
}
