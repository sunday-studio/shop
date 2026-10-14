package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/hosted/hclock"
)

func TestDevClockOnlyForTheFakeApple(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHOP_MODE", "hosted")
	t.Setenv("SHOP_DATA_DIR", dir)
	t.Setenv("SHOP_APPLE_MODE", "fake")
	t.Setenv("SHOP_APPLE_FAKE_ACK", "i-know-this-is-a-test")
	t.Setenv("SHOP_APPLE_ENVIRONMENT", "sandbox")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	clk, err := serveClock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := clk.(*hclock.Offset); !ok {
		t.Fatalf("fake mode: clock is %T, want *hclock.Offset", clk)
	}
	// live Apple (any environment) and the production environment never get a movable clock
	for _, env := range []string{"auto", "sandbox", "production"} {
		c := cfg
		c.Apple.Mode, c.Apple.Environment = "live", env
		if clk, _ := serveClock(c); clk != (hclock.System{}) {
			t.Fatalf("live/%s: clock is %T", env, clk)
		}
	}
	c := cfg
	c.Apple.Environment = "production"
	if devClockAllowed(c) {
		t.Fatal("fake + production allowed a movable clock")
	}
	c = cfg
	c.API.Environment = "production"
	if devClockAllowed(c) {
		t.Fatal("production API environment allowed a movable clock")
	}
	// the production configuration itself is refused with the fake Apple
	t.Setenv("SHOP_APPLE_ENVIRONMENT", "production")
	if _, err := loadConfig(); err == nil {
		t.Fatal("fake Apple with production accepted")
	}
}

func TestDevAdvanceClockRoute(t *testing.T) {
	off, err := hclock.NewOffset(filepath.Join(t.TempDir(), "off"))
	if err != nil {
		t.Fatal(err)
	}
	passed := ""
	h := devWithClock(func(w http.ResponseWriter, r *http.Request, what string) { passed = what }, off)
	call := func(what, body string) int {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("POST", "/admin/v1/dev/"+what, bytes.NewBufferString(body)), what)
		return rec.Code
	}
	if code := call("transaction", `{}`); code != 200 || passed != "transaction" {
		t.Fatal("other routes must reach the fake Apple", code, passed)
	}
	if code := call("advance-clock", `{"days": 8}`); code != 200 {
		t.Fatal(code)
	}
	if d := off.Offset(); d != 8*24*time.Hour {
		t.Fatal(d)
	}
	call("advance-clock", `{"hours": 1, "seconds": 30}`)
	if d := off.Offset(); d != 8*24*time.Hour+time.Hour+30*time.Second {
		t.Fatal(d)
	}
	for _, body := range []string{`{"days": -1}`, `{"days": 100000}`} {
		if code := call("advance-clock", body); code != 400 {
			t.Fatalf("%s = %d, want 400", body, code)
		}
	}
	before := off.Offset()
	if code := call("advance-clock", `{}`); code != 200 || off.Offset() != before {
		t.Fatal("an empty body must only report the time")
	}
}
