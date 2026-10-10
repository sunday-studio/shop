package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shop/internal/backup"
	"shop/internal/config"
	"shop/internal/store"
)

// backupCmd runs one backup, or with -schedule keeps running and backs up
// daily at the configured local time.
func backupCmd(ctx context.Context, log *slog.Logger, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dir := fs.String("dir", cfg.BackupDir, "backup directory, e.g. the NAS mount (env BACKUP_DIR)")
	keep := fs.Int("keep-days", cfg.BackupKeepDays, "delete snapshots older than this (env BACKUP_KEEP_DAYS)")
	schedule := fs.Bool("schedule", false, "run forever, backing up daily at BACKUP_AT (local time)")
	at := fs.String("at", cfg.BackupAt, "daily time HH:MM for -schedule (env BACKUP_AT)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	hh, mm, err := parseClock(*at)
	if err != nil {
		return err
	}

	conn, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer conn.Close()

	once := func() error {
		st := backup.Run(ctx, backup.Options{DB: conn, DataDir: cfg.DataDir, BackupDir: *dir, KeepDays: *keep})
		if !st.OK {
			log.Error("backup failed", "dir", *dir, "err", st.Error)
			return errors.New(st.Error)
		}
		log.Info("backup done", "snapshot", st.Snapshot, "bytes", st.SnapshotBytes,
			"images_copied", st.ImagesCopied, "images_total", st.ImagesTotal, "pruned", st.Pruned, "ms", st.DurationMS)
		return nil
	}
	if !*schedule {
		return once()
	}

	log.Info("backup scheduler started", "dir", *dir, "at", *at, "tz", cfg.Location.String())
	for {
		next := nextRun(time.Now().In(cfg.Location), hh, mm)
		log.Info("next backup", "at", next)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
			_ = once() // failures are recorded in the status file and logged
		}
	}
}

func parseClock(s string) (int, int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	return hh, mm, nil
}

func nextRun(now time.Time, hh, mm int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// restoreCmd replaces the database with a snapshot. Run it with the API
// stopped (see docs/restore.md).
func restoreCmd(ctx context.Context, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	dir := fs.String("dir", cfg.BackupDir, "backup directory (env BACKUP_DIR)")
	snap := fs.String("snapshot", "latest", "snapshot file name in <dir>/db, or 'latest'")
	yes := fs.Bool("yes", false, "confirm overwriting the current database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes {
		return fmt.Errorf("this replaces %s; re-run with -yes once the API is stopped", cfg.DBPath())
	}
	copied, err := backup.Restore(ctx, *dir, *snap, cfg.DBPath(), cfg.DataDir)
	if err != nil {
		return err
	}
	fmt.Printf("restored %s from %s (%d images copied back)\n", cfg.DBPath(), *snap, copied)
	return nil
}

// healthcheckCmd is the container liveness probe (the image has no curl).
// It checks that the server answers, not that every dependency is healthy.
func healthcheckCmd() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	addr := cfg.Addr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/version")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
