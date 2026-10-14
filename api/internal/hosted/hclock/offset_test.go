package hclock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOffsetAdvancesPersistsAndNeverGoesBack(t *testing.T) {
	f := filepath.Join(t.TempDir(), "off")
	o, err := NewOffset(f)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if d := o.Now().Sub(before); d > time.Second || d < -time.Second {
		t.Fatalf("zero offset drifts: %s", d)
	}
	if err := o.Advance(48 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if d := o.Now().Sub(before); d < 47*time.Hour || d > 49*time.Hour {
		t.Fatalf("offset not applied: %s", d)
	}
	for _, bad := range []time.Duration{0, -time.Hour, MaxOffset} {
		if err := o.Advance(bad); err == nil {
			t.Fatalf("Advance(%s) accepted", bad)
		}
	}
	// a restart (a new Offset on the same file) keeps the offset
	o2, err := NewOffset(f)
	if err != nil || o2.Offset() != 48*time.Hour {
		t.Fatal(err, o2.Offset())
	}
	if err := os.WriteFile(f, []byte("-5"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOffset(f); err == nil {
		t.Fatal("negative offset file accepted")
	}
}
