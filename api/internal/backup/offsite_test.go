package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/store"
)

func TestOffsiteEvaluate(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		st     OffsiteStatus
		wantOK bool
		in     string
	}{
		{"fresh", OffsiteStatus{OK: true, At: now.Add(-time.Hour), LastOKAt: now.Add(-time.Hour), Destination: "b2"}, true, "b2 copy 1h0m0s ago"},
		{"no last_ok_at falls back to at", OffsiteStatus{OK: true, At: now.Add(-2 * time.Hour)}, true, "off-site copy 2h0m0s ago"},
		{"stale", OffsiteStatus{OK: true, At: now.Add(-40 * time.Hour), LastOKAt: now.Add(-40 * time.Hour)}, false, "stale"},
		{"failed, earlier success", OffsiteStatus{At: now.Add(-time.Hour), LastOKAt: now.Add(-20 * time.Hour), Error: "boom"}, false, "last success 20h0m0s ago): boom"},
		{"failed, never succeeded", OffsiteStatus{At: now, Error: "boom"}, false, "never succeeded"},
	}
	for _, c := range cases {
		ok, d := c.st.Evaluate(now)
		if ok != c.wantOK || !strings.Contains(d, c.in) {
			t.Errorf("%s: ok=%v detail=%q, want ok=%v containing %q", c.name, ok, d, c.wantOK, c.in)
		}
	}
}

func TestReadOffsiteStatus(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadOffsiteStatus(dir); !os.IsNotExist(err) {
		t.Fatalf("missing file should be ErrNotExist, got %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, OffsiteFile), []byte(`{"ok":true,"at":"2026-10-12T04:00:00Z","last_ok_at":"2026-10-12T04:00:00Z","destination":"x","duration_ms":12}`), 0o644)
	st, err := ReadOffsiteStatus(dir)
	if err != nil || !st.OK || st.Destination != "x" || st.LastOKAt.IsZero() {
		t.Fatalf("%+v %v", st, err)
	}
}

// fakeRclone stands in for rclone: copy/sync/copyto/check/lsf operate on plain folders, and
// FAKE_RCLONE_FAIL makes every call fail. The remote "dest:" maps to $FAKE_REMOTE_ROOT.
const fakeRclone = `#!/bin/sh
[ -n "$FAKE_RCLONE_FAIL" ] && { echo "$FAKE_RCLONE_FAIL" >&2; exit 1; }
verb=$1; src=$2; dst=$3
m() { case "$1" in dest:*) echo "$FAKE_REMOTE_ROOT/${1#dest:}";; *) echo "$1";; esac; }
src=$(m "$src"); dst=$(m "$dst")
case "$verb" in
  copy|sync)
    mkdir -p "$dst"
    [ "$verb" = sync ] && for f in "$dst"/*; do [ -e "$f" ] && [ ! -e "$src/$(basename "$f")" ] && rm -rf "$f"; done
    cp -R "$src"/. "$dst"/ ;;
  copyto) mkdir -p "$(dirname "$dst")"; cp "$src" "$dst" ;;
  check) for f in "$src"/*; do [ -e "$dst/$(basename "$f")" ] || { echo "missing $f" >&2; exit 1; }; done ;;
  *) echo "unsupported $verb" >&2; exit 2 ;;
esac
`

func runOffsite(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("../../../deploy/offsite/offsite.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skip("deploy/offsite/offsite.sh not found")
	}
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestOffsiteScriptAndRestoreFromReturnedCopy(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	ctx := context.Background()
	data, bdir := t.TempDir(), t.TempDir()
	db, err := store.Open(filepath.Join(data, "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shops (name, created_at, updated_at) VALUES ('Jumbo', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(data, "images", "receipts", "ab", "abc.jpg")
	_ = os.MkdirAll(filepath.Dir(img), 0o755)
	_ = os.WriteFile(img, []byte("jpeg-bytes"), 0o644)
	if st := Run(ctx, Options{DB: db, DataDir: data, BackupDir: bdir, KeepDays: 30}); !st.OK {
		t.Fatalf("backup: %+v", st)
	}

	bin, remote := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "rclone"), []byte(fakeRclone), 0o755); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(t.TempDir(), OffsiteFile)
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_REMOTE_ROOT=" + remote,
		"OFFSITE_REMOTE=dest:shop", "OFFSITE_LABEL=testdest",
		"OFFSITE_SRC_DIR=" + bdir, "OFFSITE_STATUS_FILE=" + status,
	}

	// Without a destination the script refuses to run once, and writes a failed status.
	if out, err := runOffsite(t, append(env, "OFFSITE_REMOTE="), "once"); err == nil {
		t.Fatalf("once without OFFSITE_REMOTE should fail: %s", out)
	}
	st, err := ReadOffsiteStatus(filepath.Dir(status))
	if err != nil || st.OK || !strings.Contains(st.Error, "OFFSITE_REMOTE") {
		t.Fatalf("status after config error: %+v %v", st, err)
	}

	// A good run: status ok, files arrived, label (not the remote string) is reported.
	if out, err := runOffsite(t, env, "once"); err != nil {
		t.Fatalf("once: %v\n%s", err, out)
	}
	st, err = ReadOffsiteStatus(filepath.Dir(status))
	if err != nil || !st.OK || st.Destination != "testdest" || st.LastOKAt.IsZero() || !strings.HasPrefix(st.Snapshot, "shop-") {
		t.Fatalf("status after sync: %+v %v", st, err)
	}
	if ok, _ := st.Evaluate(time.Now()); !ok {
		t.Fatal("fresh sync should be healthy")
	}
	if _, err := os.Stat(filepath.Join(remote, "shop", "images", "receipts", "ab", "abc.jpg")); err != nil {
		t.Fatalf("image not copied: %v", err)
	}

	// A failing destination keeps the last success time and reports the error.
	out, err := runOffsite(t, append(env, "FAKE_RCLONE_FAIL=connection refused to a \"quoted\" host"), "once")
	if err == nil {
		t.Fatalf("failure should exit non-zero: %s", out)
	}
	st2, err := ReadOffsiteStatus(filepath.Dir(status))
	if err != nil || st2.OK || !st2.LastOKAt.Equal(st.LastOKAt) || !strings.Contains(st2.Error, "connection refused") {
		t.Fatalf("status after failure: %+v %v", st2, err)
	}

	// Disaster: the backup folder is gone. Pull the copy back into a new, empty folder
	// and restore from it into a new data directory.
	returned := t.TempDir()
	if out, err := runOffsite(t, append(env, "OFFSITE_SRC_DIR="+returned), "pull"); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	newData := t.TempDir()
	n, err := Restore(ctx, returned, "latest", filepath.Join(newData, "shop.db"), newData)
	if err != nil || n != 1 {
		t.Fatalf("restore from returned copy: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(newData, "images", "receipts", "ab", "abc.jpg"))
	if err != nil || string(got) != "jpeg-bytes" {
		t.Fatalf("image after restore: %q %v", got, err)
	}
	rdb, err := store.Open(filepath.Join(newData, "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	var name string
	if err := rdb.QueryRow(`SELECT name FROM shops`).Scan(&name); err != nil || name != "Jumbo" {
		t.Fatalf("shop after restore: %q %v", name, err)
	}

	// Pull from an empty destination must fail loudly, not "succeed" with nothing.
	if out, err := runOffsite(t, append(env, "OFFSITE_SRC_DIR="+t.TempDir(), "OFFSITE_REMOTE=dest:nothing-here"), "pull"); err == nil {
		t.Fatalf("pull of an empty destination should fail: %s", out)
	}
}
