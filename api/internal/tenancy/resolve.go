package tenancy

import (
	"context"
	"strings"
	"sync"

	"shop/internal/auth"
)

// SingleResolver sends every request to the one tenant of single mode. Whether the token is valid is
// decided by that tenant's own token table, exactly as before the seam existed.
type SingleResolver struct{}

func (SingleResolver) Resolve(context.Context, string) (string, error) { return DefaultID, nil }

// DirectoryResolver resolves a bearer token through a Directory.
type DirectoryResolver struct{ Dir Directory }

func (r DirectoryResolver) Resolve(ctx context.Context, bearer string) (string, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return "", ErrUnknownToken
	}
	return r.Dir.HouseholdForToken(ctx, auth.Hash(bearer))
}

// MemoryDirectory is a Directory held in memory. It is what tests and small embeddings use; a
// hosted deployment replaces it with its control database.
type MemoryDirectory struct {
	mu sync.RWMutex
	m  map[string]string
}

func NewMemoryDirectory() *MemoryDirectory { return &MemoryDirectory{m: map[string]string{}} }

// Register records that the token with this hash belongs to the household.
func (d *MemoryDirectory) Register(tokenHash, householdID string) {
	d.mu.Lock()
	d.m[tokenHash] = householdID
	d.mu.Unlock()
}

// Forget removes one token.
func (d *MemoryDirectory) Forget(tokenHash string) {
	d.mu.Lock()
	delete(d.m, tokenHash)
	d.mu.Unlock()
}

// ForgetHousehold removes every token of a household.
func (d *MemoryDirectory) ForgetHousehold(householdID string) {
	d.mu.Lock()
	for h, id := range d.m {
		if id == householdID {
			delete(d.m, h)
		}
	}
	d.mu.Unlock()
}

func (d *MemoryDirectory) HouseholdForToken(_ context.Context, tokenHash string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if id, ok := d.m[tokenHash]; ok {
		return id, nil
	}
	return "", ErrUnknownToken
}
