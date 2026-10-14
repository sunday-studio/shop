package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/store"
)

func mkDB(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func count(t *testing.T, path, table string) int {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The hosted backup takes the control database and every household into one set, and a restore puts
// the set back without resurrecting a household that the restored control database says is erased.
func TestHostedBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	data, bdir := t.TempDir(), t.TempDir()
	p := backupPaths{DataDir: data, ControlDB: filepath.Join(data, "control.db"), BackupDir: bdir, KeepDays: 30, Loc: time.UTC}
	mkDB(t, p.ControlDB, `CREATE TABLE households (id TEXT PRIMARY KEY, status TEXT)`,
		`INSERT INTO households VALUES ('aaa','active'),('bbb','deleted'),('ccc','active')`)
	for _, id := range []string{"aaa", "bbb"} {
		dir := filepath.Join(data, "households", id)
		mkDB(t, filepath.Join(dir, "shop.db"), `CREATE TABLE receipts (id INTEGER)`, `INSERT INTO receipts VALUES (1),(2)`)
		if err := os.MkdirAll(filepath.Join(dir, "images", "receipts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "images", "receipts", "x.jpg"), []byte("img-"+id), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := backupSet(ctx, p, time.Date(2026, 10, 14, 3, 30, 0, 0, time.UTC))
	if !st.OK || st.Households != 2 || st.Snapshot != "shop-20261014-033000.db" {
		t.Fatalf("backup: %+v", st)
	}
	for _, f := range []string{"db/shop-20261014-033000.db", "households/aaa/db/shop-20261014-033000.db", "households/aaa/images/receipts/x.jpg", "households/bbb/db/shop-20261014-033000.db", "last_backup.json"} {
		if _, err := os.Stat(filepath.Join(bdir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}

	// disaster: the household database is damaged, the control database lost its state, bbb's folder came back
	mkDB(t, filepath.Join(data, "households", "aaa", "shop.db"), `DELETE FROM receipts`)
	if err := os.Remove(filepath.Join(data, "households", "aaa", "images", "receipts", "x.jpg")); err != nil {
		t.Fatal(err)
	}
	mkDB(t, p.ControlDB, `DELETE FROM households`)
	res, err := restoreSet(ctx, p, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 || res.Reerased != 1 || res.Images != 1 || len(res.Missing) != 1 || res.Missing[0] != "ccc" {
		t.Fatalf("restore: %+v", res)
	}
	if n := count(t, filepath.Join(data, "households", "aaa", "shop.db"), "receipts"); n != 2 {
		t.Fatalf("household data not restored: %d rows", n)
	}
	if n := count(t, p.ControlDB, "households"); n != 3 {
		t.Fatalf("control database not restored: %d", n)
	}
	if _, err := os.Stat(filepath.Join(data, "households", "bbb")); !os.IsNotExist(err) {
		t.Fatalf("an erased household was resurrected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "households", "aaa", "images", "receipts", "x.jpg")); err != nil {
		t.Fatalf("image not restored: %v", err)
	}

	// a missing backup folder is an error and says so in the status
	p.BackupDir = filepath.Join(bdir, "nope")
	if st := backupSet(ctx, p, time.Now()); st.OK {
		t.Fatal("backup into a missing folder reported success")
	}
}
