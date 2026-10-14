package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shop/internal/backup"
	"shop/internal/config"
	"shop/internal/store"
)

// The hosted data layout is the control database (SHOP_CONTROL_DB) plus one folder per household
// (<data dir>/households/<id>/shop.db and images/). A backup set is written into BACKUP_DIR as
//
//	db/shop-<ts>.db                          snapshot of the control database (VACUUM INTO, integrity checked)
//	households/<id>/db/shop-<ts>.db          snapshot of each household database, same <ts>
//	households/<id>/images/...               mirror of the household's images (append-only)
//	last_backup.json                         written LAST: the set is complete when it says ok
//
// All snapshots of one run share <ts>; the control database is taken first. Every snapshot is
// individually consistent (SQLite takes it while the server keeps writing). Across files the set is
// consistent to within the minutes the run takes: a household created in between is picked up by the
// next run, and a restore reconciles the folders with the restored control database (see restoreSet).
// The control snapshot name matches the single-household pattern so the off-site script and its
// checks work unchanged; it also syncs households/.

var householdID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type setStatus struct {
	OK         bool      `json:"ok"`
	At         time.Time `json:"at"`
	AtUnix     int64     `json:"at_unix"`            // for the shell monitor
	Snapshot   string    `json:"snapshot,omitempty"` // the control snapshot of this set
	Households int       `json:"households"`
	Failed     []string  `json:"failed_households,omitempty"`
	Pruned     int       `json:"pruned"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

type backupPaths struct {
	DataDir, ControlDB, BackupDir string
	KeepDays                      int
	At                            string
	Loc                           *time.Location
}

func backupPathsFromEnv() (backupPaths, error) {
	app, err := config.Load()
	if err != nil {
		return backupPaths{}, err
	}
	return backupPaths{DataDir: app.DataDir, ControlDB: env("SHOP_CONTROL_DB", filepath.Join(app.DataDir, "control.db")),
		BackupDir: app.BackupDir, KeepDays: app.BackupKeepDays, At: app.BackupAt, Loc: app.Location}, nil
}

// backupSet makes one backup set. It never touches the live files except to read them.
func backupSet(ctx context.Context, p backupPaths, now time.Time) setStatus {
	start := time.Now()
	st := setStatus{At: now.UTC(), AtUnix: now.Unix()}
	err := func() error {
		if fi, err := os.Stat(p.BackupDir); err != nil || !fi.IsDir() {
			return fmt.Errorf("backup directory not available: %s (is the volume mounted?)", p.BackupDir)
		}
		if _, err := os.Stat(p.ControlDB); err != nil {
			return fmt.Errorf("control database: %w", err)
		}
		ts := now.UTC().Format("20060102-150405")
		name := "shop-" + ts + ".db"

		ctl, err := store.Open(p.ControlDB)
		if err != nil {
			return err
		}
		defer ctl.Close()
		if err := snapshotTo(ctx, ctl, filepath.Join(p.BackupDir, "db"), name); err != nil {
			return fmt.Errorf("control database: %w", err)
		}
		st.Snapshot = name
		pruned, err := backup.Prune(filepath.Join(p.BackupDir, "db"), p.KeepDays, now)
		if err != nil {
			return err
		}
		st.Pruned += pruned

		ids, err := householdIDs(p.DataDir)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := backupHousehold(ctx, p, id, name, now, &st); err != nil {
				st.Failed = append(st.Failed, id)
			} else {
				st.Households++
			}
		}
		if len(st.Failed) > 0 {
			return fmt.Errorf("%d household(s) could not be backed up: %s", len(st.Failed), strings.Join(st.Failed, ", "))
		}
		return nil
	}()
	st.OK = err == nil
	if err != nil {
		st.Error = err.Error()
	}
	st.DurationMS = time.Since(start).Milliseconds()
	_ = writeJSONAtomic(filepath.Join(p.BackupDir, "last_backup.json"), st)
	return st
}

func snapshotTo(ctx context.Context, db *sql.DB, dir, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	tmp := final + ".partial"
	_ = os.Remove(tmp)
	if err := backup.Snapshot(ctx, db, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := backup.IntegrityCheck(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("integrity: %w", err)
	}
	return os.Rename(tmp, final)
}

func backupHousehold(ctx context.Context, p backupPaths, id, name string, now time.Time, st *setStatus) error {
	src := filepath.Join(p.DataDir, "households", id)
	if _, err := os.Stat(filepath.Join(src, "shop.db")); err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(src, "shop.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	dst := filepath.Join(p.BackupDir, "households", id)
	if err := snapshotTo(ctx, db, filepath.Join(dst, "db"), name); err != nil {
		return err
	}
	if _, _, err := backup.MirrorImages(filepath.Join(src, "images"), filepath.Join(dst, "images")); err != nil {
		return err
	}
	n, err := backup.Prune(filepath.Join(dst, "db"), p.KeepDays, now)
	st.Pruned += n
	return err
}

func householdIDs(dataDir string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(dataDir, "households"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() && householdID.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// backupCmd: `shop-hosted backup [-schedule] [-dir D] [-keep-days N] [-at HH:MM]`.
func backupCmd(ctx context.Context, log *slog.Logger, args []string) error {
	p, err := backupPathsFromEnv()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.StringVar(&p.BackupDir, "dir", p.BackupDir, "backup directory (env BACKUP_DIR)")
	fs.IntVar(&p.KeepDays, "keep-days", p.KeepDays, "delete snapshots older than this (env BACKUP_KEEP_DAYS)")
	fs.StringVar(&p.At, "at", p.At, "daily time HH:MM for -schedule (env BACKUP_AT)")
	schedule := fs.Bool("schedule", false, "run forever, backing up daily at BACKUP_AT (local time)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	hh, mm, err := parseClockHM(p.At)
	if err != nil {
		return err
	}
	once := func() error {
		st := backupSet(ctx, p, time.Now())
		if !st.OK {
			log.Error("backup failed", "dir", p.BackupDir, "err", st.Error)
			return errors.New(st.Error)
		}
		log.Info("backup done", "snapshot", st.Snapshot, "households", st.Households, "pruned", st.Pruned, "ms", st.DurationMS)
		return nil
	}
	if !*schedule {
		return once()
	}
	log.Info("backup scheduler started", "dir", p.BackupDir, "at", p.At, "tz", p.Loc.String())
	for {
		now := time.Now().In(p.Loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		log.Info("next backup", "at", next)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
			_ = once()
		}
	}
}

func parseClockHM(s string) (int, int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	return hh, mm, nil
}

// restoreCmd: `shop-hosted restore -yes [-snapshot latest|shop-<ts>.db]`. The server must be stopped.
func restoreCmd(ctx context.Context, args []string) error {
	p, err := backupPathsFromEnv()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.StringVar(&p.BackupDir, "dir", p.BackupDir, "backup directory (env BACKUP_DIR)")
	snap := fs.String("snapshot", "latest", "control snapshot name in <dir>/db, or 'latest'")
	yes := fs.Bool("yes", false, "confirm replacing the control database and the household databases")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes {
		return fmt.Errorf("this replaces %s and the household databases under %s; re-run with -yes once the server is stopped", p.ControlDB, p.DataDir)
	}
	res, err := restoreSet(ctx, p, *snap)
	if err != nil {
		return err
	}
	fmt.Printf("restored control database from %s: %d households restored, %d images copied back, %d erased households removed again, %d households had no backup\n",
		res.Snapshot, res.Restored, res.Images, res.Reerased, len(res.Missing))
	for _, id := range res.Missing {
		fmt.Printf("  no backup for household %s (created after the set was taken?)\n", id)
	}
	return nil
}

type restoreResult struct {
	Snapshot string
	Restored int
	Images   int
	Reerased int
	Missing  []string
}

// restoreSet puts a backup set back: the control database first, then every household that the
// restored control database knows and that is not deleted (newest snapshot of each, plus missing
// images). Households that the restored control database marks deleted are removed from the data
// folder again, so a restore never resurrects an erased household. The server must be stopped.
func restoreSet(ctx context.Context, p backupPaths, snapshot string) (restoreResult, error) {
	var res restoreResult
	if _, err := backup.Restore(ctx, p.BackupDir, snapshot, p.ControlDB, ""); err != nil {
		return res, err
	}
	// backup.Restore copied no images (the control backup has none); name the snapshot we used.
	snaps, _ := backup.Snapshots(filepath.Join(p.BackupDir, "db"))
	res.Snapshot = snapshot
	if (snapshot == "" || snapshot == "latest") && len(snaps) > 0 {
		res.Snapshot = snaps[0]
	}
	ctl, err := store.Open(p.ControlDB)
	if err != nil {
		return res, err
	}
	defer ctl.Close()
	rows, err := ctl.QueryContext(ctx, `SELECT id, status FROM households`)
	if err != nil {
		return res, err
	}
	type hh struct{ id, status string }
	var hs []hh
	for rows.Next() {
		var h hh
		if err := rows.Scan(&h.id, &h.status); err != nil {
			rows.Close()
			return res, err
		}
		hs = append(hs, h)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	rows.Close()
	for _, h := range hs {
		if !householdID.MatchString(h.id) {
			continue
		}
		dir := filepath.Join(p.DataDir, "households", h.id)
		if h.status == "deleted" {
			if _, err := os.Stat(dir); err == nil {
				if err := os.RemoveAll(dir); err != nil {
					return res, err
				}
				res.Reerased++
			}
			continue
		}
		n, err := backup.Restore(ctx, filepath.Join(p.BackupDir, "households", h.id), "latest", filepath.Join(dir, "shop.db"), dir)
		if err != nil {
			res.Missing = append(res.Missing, h.id)
			continue
		}
		res.Restored++
		res.Images += n
	}
	return res, nil
}
