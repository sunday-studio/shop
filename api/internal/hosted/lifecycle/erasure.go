package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Layout is where a household's files live (contract/plan data layout):
//
//	DataDir/households/<id>/shop.db (+ -wal, -shm) and images/
//	BackupDir/households/<id>/{db,images}
//	ExportDir/<id>/*.zip
type Layout struct {
	DataDir   string
	BackupDir string
	ExportDir string
}

// ErasureItem is one thing to delete.
type ErasureItem struct {
	Kind string // database, images, backups, exports
	Path string
}

// ErasurePlan lists everything that goes when a household is erased. The control database keeps
// the household row (tombstone, no personal data) and the billing ledger.
type ErasurePlan struct {
	HouseholdID string
	Items       []ErasureItem
}

// Paths returns the plan's paths.
func (p ErasurePlan) Paths() []string {
	var out []string
	for _, i := range p.Items {
		out = append(out, i.Path)
	}
	return out
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// PlanErasure builds the plan for a household. It is a pure function of the layout.
func PlanErasure(l Layout, householdID string) (ErasurePlan, error) {
	if !uuidRe.MatchString(householdID) {
		return ErasurePlan{}, fmt.Errorf("refusing to plan erasure for malformed household id %q", householdID)
	}
	p := ErasurePlan{HouseholdID: householdID}
	if l.DataDir != "" {
		dir := filepath.Join(l.DataDir, "households", householdID)
		p.Items = append(p.Items,
			ErasureItem{"database", filepath.Join(dir, "shop.db")},
			ErasureItem{"database", filepath.Join(dir, "shop.db-wal")},
			ErasureItem{"database", filepath.Join(dir, "shop.db-shm")},
			ErasureItem{"images", filepath.Join(dir, "images")},
			ErasureItem{"database", dir}, // the folder itself, last
		)
	}
	if l.BackupDir != "" {
		p.Items = append(p.Items, ErasureItem{"backups", filepath.Join(l.BackupDir, "households", householdID)})
	}
	if l.ExportDir != "" {
		p.Items = append(p.Items, ErasureItem{"exports", filepath.Join(l.ExportDir, householdID)})
	}
	return p, nil
}

// Eraser executes an ErasurePlan. Erasing something that is already gone is not an error.
type Eraser interface {
	Erase(ctx context.Context, plan ErasurePlan) error
}

// OSEraser deletes the planned paths from disk. Every path must lie strictly inside one of the
// layout's roots; anything else is refused before the first deletion.
type OSEraser struct{ Layout Layout }

func (e OSEraser) Erase(_ context.Context, plan ErasurePlan) error {
	if !uuidRe.MatchString(plan.HouseholdID) {
		return errors.New("malformed household id")
	}
	roots := []string{}
	for _, r := range []string{e.Layout.DataDir, e.Layout.BackupDir, e.Layout.ExportDir} {
		if r != "" {
			roots = append(roots, filepath.Clean(r))
		}
	}
	for _, it := range plan.Items {
		p := filepath.Clean(it.Path)
		ok := false
		for _, r := range roots {
			if strings.HasPrefix(p, r+string(filepath.Separator)) && strings.Contains(p, plan.HouseholdID) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("refusing to delete %q: outside the data roots or not this household's", it.Path)
		}
	}
	for _, it := range plan.Items {
		if err := os.RemoveAll(filepath.Clean(it.Path)); err != nil {
			return err
		}
	}
	return nil
}

// FakeEraser records plans instead of deleting. Set Fail to make Erase return an error.
type FakeEraser struct {
	mu     sync.Mutex
	Erased []ErasurePlan
	Fail   error
}

func (f *FakeEraser) Erase(_ context.Context, plan ErasurePlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return f.Fail
	}
	f.Erased = append(f.Erased, plan)
	return nil
}

// Count is the number of plans erased.
func (f *FakeEraser) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Erased)
}
