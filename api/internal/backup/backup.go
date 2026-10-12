// Package backup snapshots the SQLite database and mirrors images to a
// backup directory (a local folder, an external disk or a network mount), and restores from it.
//
// Layout of the backup directory:
//
//	db/shop-20261010-033000.db   consistent snapshots (VACUUM INTO)
//	images/receipts/ab/<sha>.jpg   content-addressed, append-only
//	images/products/...
//	last_backup.json               status of the most recent run
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	snapshotPrefix = "shop-"
	snapshotLayout = "20060102-150405"
	statusFile     = "last_backup.json"
	minKeep        = 3 // never prune below this many snapshots
)

// Status describes a backup run. A copy is written to the backup dir and
// to the data dir (so the API can report it without seeing the backup folder).
type Status struct {
	OK            bool      `json:"ok"`
	At            time.Time `json:"at"`
	Snapshot      string    `json:"snapshot,omitempty"`
	SnapshotBytes int64     `json:"snapshot_bytes,omitempty"`
	ImagesCopied  int       `json:"images_copied"`
	ImagesTotal   int       `json:"images_total"`
	Pruned        int       `json:"pruned"`
	Error         string    `json:"error,omitempty"`
	DurationMS    int64     `json:"duration_ms"`
}

type Options struct {
	DB        *sql.DB
	DataDir   string // contains images/
	BackupDir string
	KeepDays  int
	Now       func() time.Time
}

// Run performs one backup and records its status.
func Run(ctx context.Context, o Options) Status {
	if o.Now == nil {
		o.Now = time.Now
	}
	start := o.Now()
	st := Status{At: start.UTC()}
	err := run(ctx, o, &st)
	st.DurationMS = time.Since(start).Milliseconds()
	st.OK = err == nil
	if err != nil {
		st.Error = err.Error()
	}
	// Record status in both places; ignore failures writing to a missing backup folder.
	_ = writeStatus(filepath.Join(o.DataDir, "backup_status.json"), st)
	if err == nil || !errors.Is(err, errNoBackupDir) {
		_ = writeStatus(filepath.Join(o.BackupDir, statusFile), st)
	}
	return st
}

var errNoBackupDir = errors.New("backup directory not available")

func run(ctx context.Context, o Options, st *Status) error {
	if fi, err := os.Stat(o.BackupDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s (is the folder or drive mounted?)", errNoBackupDir, o.BackupDir)
	}
	dbDir := filepath.Join(o.BackupDir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return err
	}

	name := snapshotPrefix + o.Now().UTC().Format(snapshotLayout) + ".db"
	final := filepath.Join(dbDir, name)
	tmp := final + ".partial"
	_ = os.Remove(tmp)
	if err := Snapshot(ctx, o.DB, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("snapshot: %w", err)
	}
	if err := IntegrityCheck(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("snapshot integrity: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	if fi, err := os.Stat(final); err == nil {
		st.SnapshotBytes = fi.Size()
	}
	st.Snapshot = name

	copied, total, err := MirrorImages(filepath.Join(o.DataDir, "images"), filepath.Join(o.BackupDir, "images"))
	st.ImagesCopied, st.ImagesTotal = copied, total
	if err != nil {
		return fmt.Errorf("images: %w", err)
	}

	pruned, err := Prune(dbDir, o.KeepDays, o.Now())
	st.Pruned = pruned
	return err
}

// Snapshot writes a consistent copy of the live database to path. VACUUM
// INTO works while the API keeps writing (WAL) and produces a compact file.
func Snapshot(ctx context.Context, db *sql.DB, path string) error {
	_, err := db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// IntegrityCheck opens a database file read-only and runs PRAGMA integrity_check.
func IntegrityCheck(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("integrity_check: %s", res)
	}
	return nil
}

// MirrorImages copies files from src that are missing in dst. Images are
// content-addressed, so an existing file never needs updating, and files
// are never deleted from dst: older snapshots may still reference them.
func MirrorImages(src, dst string) (copied, total int, err error) {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}
		total++
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if err := copyFile(p, target); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, total, err
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// Snapshots lists snapshot files in dbDir, newest first.
func Snapshots(dbDir string) ([]string, error) {
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, snapshotPrefix) && strings.HasSuffix(n, ".db") {
			out = append(out, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out))) // timestamp names sort chronologically
	return out, nil
}

func snapshotTime(name string) (time.Time, bool) {
	ts := strings.TrimSuffix(strings.TrimPrefix(name, snapshotPrefix), ".db")
	t, err := time.Parse(snapshotLayout, ts)
	return t, err == nil
}

// Prune deletes snapshots older than keepDays, always keeping the newest minKeep.
func Prune(dbDir string, keepDays int, now time.Time) (int, error) {
	if keepDays <= 0 {
		return 0, nil
	}
	snaps, err := Snapshots(dbDir)
	if err != nil {
		return 0, err
	}
	cutoff := now.AddDate(0, 0, -keepDays)
	pruned := 0
	for i, name := range snaps {
		if i < minKeep {
			continue
		}
		if t, ok := snapshotTime(name); ok && t.Before(cutoff) {
			if err := os.Remove(filepath.Join(dbDir, name)); err != nil {
				return pruned, err
			}
			pruned++
		}
	}
	return pruned, nil
}

// ReadStatus loads a status file written by Run.
func ReadStatus(path string) (Status, error) {
	var st Status
	b, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func writeStatus(path string, st Status) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Restore replaces the database at dbPath with a snapshot and copies back
// any images missing from dataDir. The API must not be running.
func Restore(ctx context.Context, backupDir, snapshot, dbPath, dataDir string) (int, error) {
	dbDir := filepath.Join(backupDir, "db")
	if snapshot == "" || snapshot == "latest" {
		snaps, err := Snapshots(dbDir)
		if err != nil {
			return 0, err
		}
		if len(snaps) == 0 {
			return 0, fmt.Errorf("no snapshots in %s", dbDir)
		}
		snapshot = snaps[0]
	}
	src := filepath.Join(dbDir, filepath.Base(snapshot))
	if err := IntegrityCheck(ctx, src); err != nil {
		return 0, fmt.Errorf("%s: %w", src, err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	if err := copyFile(src, dbPath); err != nil {
		return 0, err
	}
	copied, _, err := MirrorImages(filepath.Join(backupDir, "images"), filepath.Join(dataDir, "images"))
	return copied, err
}
