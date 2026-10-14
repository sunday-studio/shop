package tenantapp

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"shop/internal/backup"
	"shop/internal/store"
	"shop/internal/tenancy"
)

// BackupHousehold snapshots one household's database and mirrors its images into backupDir, the same
// way the single-household backup does. Each household has its own backup folder, so a folder can be
// erased with the household (tenancy.Multi.Erase removes BackupDir(id)).
func (rt *Runtime) BackupHousehold(ctx context.Context, t *tenancy.Tenant, backupDir string) backup.Status {
	return backup.Run(ctx, backup.Options{DB: t.DB, DataDir: t.Dir, BackupDir: backupDir, KeepDays: rt.Cfg.BackupKeepDays})
}

// BackupAll backs up every household of a Multi provider one after another and returns the result per
// household. One household failing does not stop the others.
func (rt *Runtime) BackupAll(ctx context.Context, m *tenancy.Multi) (map[string]backup.Status, error) {
	if m.BackupRoot() == "" {
		return nil, errors.New("tenantapp: the provider has no BackupRoot")
	}
	// The root must exist already: a missing folder usually means an unmounted drive, which a backup must notice
	// instead of quietly filling the system disk. A household's own folder inside it is created here.
	if _, err := os.Stat(m.BackupRoot()); err != nil {
		return nil, fmt.Errorf("backup root not available: %w", err)
	}
	ids, err := m.IDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]backup.Status, len(ids))
	for _, id := range ids {
		t, release, err := m.Acquire(ctx, id)
		if err != nil {
			out[id] = backup.Status{OK: false, At: time.Now().UTC(), Error: err.Error()}
			continue
		}
		if err := os.MkdirAll(m.BackupDir(id), 0o755); err != nil {
			out[id] = backup.Status{OK: false, At: time.Now().UTC(), Error: err.Error()}
		} else {
			out[id] = rt.BackupHousehold(ctx, t, m.BackupDir(id))
		}
		release()
	}
	return out, nil
}

// Manifest describes an export archive.
type Manifest struct {
	Format        string    `json:"format"` // "shop-export-1"
	ExportedAt    time.Time `json:"exported_at"`
	SchemaVersion int64     `json:"schema_version"`
	Files         []string  `json:"files"`
}

// Export streams everything one household owns as a zip: manifest.json, shop.db (a consistent copy of
// its database) and images/ (receipt and product photos). The archive holds this household only.
func Export(ctx context.Context, t *tenancy.Tenant, w io.Writer) error {
	tmp, err := os.MkdirTemp(t.Dir, "export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, "shop.db")
	if err := backup.Snapshot(ctx, t.DB, snap); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	version, err := store.MigrationVersion(ctx, t.DB)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	man := Manifest{Format: "shop-export-1", ExportedAt: time.Now().UTC(), SchemaVersion: version}
	add := func(name, path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, f)
		man.Files = append(man.Files, name)
		return err
	}
	if err := add("shop.db", snap); err != nil {
		return err
	}
	if t.ImagesDir != "" {
		err = filepath.WalkDir(t.ImagesDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(t.ImagesDir, p)
			if err != nil {
				return err
			}
			return add("images/"+filepath.ToSlash(rel), p)
		})
		if err != nil {
			return err
		}
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(mw).Encode(man); err != nil {
		return err
	}
	return zw.Close()
}
