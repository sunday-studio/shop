package httpapi_test

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/apitypes"
)

func TestHealthReportsOffsite(t *testing.T) {
	e := newEnv(t)
	health := func() (int, apitypes.Check, bool) {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
		h := decodeAs[apitypes.Health](t, rec)
		c, ok := h.Checks["backup"]
		return rec.Code, c, ok
	}
	put := func(name, body string) {
		if err := os.WriteFile(filepath.Join(e.dataDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format(time.RFC3339) }

	// Nothing configured: no backup check at all, still healthy.
	if code, _, ok := health(); code != 200 || ok {
		t.Fatalf("no backups, no off-site: %d present=%v", code, ok)
	}
	// A fresh local backup, no off-site step: unchanged behaviour.
	put("backup_status.json", fmt.Sprintf(`{"ok":true,"at":%q,"snapshot":"shop-x.db"}`, ts(2*time.Hour)))
	if code, c, _ := health(); code != 200 || !c.Ok || strings.Contains(*c.Detail, "copy") {
		t.Fatalf("local only: %d %+v", code, c)
	}
	// Off-site fresh: still healthy, and mentioned.
	put("offsite_status.json", fmt.Sprintf(`{"ok":true,"at":%q,"last_ok_at":%q,"destination":"b2crypt"}`, ts(time.Hour), ts(time.Hour)))
	if code, c, _ := health(); code != 200 || !c.Ok || !strings.Contains(*c.Detail, "b2crypt copy") {
		t.Fatalf("off-site fresh: %d %+v", code, c)
	}
	// Off-site failed: unhealthy even though the local backup is fine, and the error is shown.
	put("offsite_status.json", fmt.Sprintf(`{"ok":false,"at":%q,"last_ok_at":%q,"destination":"b2crypt","error":"images: couldn't connect"}`, ts(10*time.Minute), ts(30*time.Hour)))
	if code, c, _ := health(); code != 503 || c.Ok || !strings.Contains(*c.Detail, "couldn't connect") {
		t.Fatalf("off-site failed: %d %+v", code, c)
	}
	// Off-site succeeded long ago: stale.
	put("offsite_status.json", fmt.Sprintf(`{"ok":true,"at":%q,"last_ok_at":%q,"destination":"sftp"}`, ts(50*time.Hour), ts(50*time.Hour)))
	if code, c, _ := health(); code != 503 || c.Ok || !strings.Contains(*c.Detail, "stale") {
		t.Fatalf("off-site stale: %d %+v", code, c)
	}
	// Off-site status alone (local status file missing) is still reported.
	_ = os.Remove(filepath.Join(e.dataDir, "backup_status.json"))
	put("offsite_status.json", fmt.Sprintf(`{"ok":true,"at":%q,"last_ok_at":%q}`, ts(time.Hour), ts(time.Hour)))
	if code, c, ok := health(); code != 200 || !ok || !c.Ok || !strings.Contains(*c.Detail, "off-site copy") {
		t.Fatalf("off-site only: %d %+v", code, c)
	}
	// A damaged status file is reported, not ignored.
	put("offsite_status.json", `{not json`)
	if code, c, _ := health(); code != 503 || c.Ok {
		t.Fatalf("broken status file: %d %+v", code, c)
	}
}
