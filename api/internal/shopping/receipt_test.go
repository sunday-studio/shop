package shopping

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/match"
	"shop/internal/ocr"
	"shop/internal/receipt"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/store/db"
)

func testService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return New(conn, match.NewMatcher(conn), stats.New(conn, time.UTC)).WithLocation(time.UTC), ctx
}

func ts(y int, m time.Month, d, h int) string {
	return store.TS(time.Date(y, m, d, h, 0, 0, 0, time.UTC))
}

func TestEligibleWindow(t *testing.T) {
	s, _ := testService(t)
	conf := ts(2026, 10, 11, 15)
	cases := []struct {
		name      string
		status    string
		confirmed string
		bought    string
		want      bool
	}{
		{"same day", "ok", conf, ts(2026, 10, 11, 0), true},
		{"3 days, date only", "ok", conf, ts(2026, 10, 8, 0), true},
		{"4 days", "ok", conf, ts(2026, 10, 7, 23), false},
		{"two weeks", "ok", conf, ts(2026, 9, 27, 12), false},
		{"far future date", "ok", conf, ts(2026, 10, 20, 0), false},
		{"unconfirmed", "needs_review", "", ts(2026, 10, 11, 0), false},
		{"no date", "ok", conf, "", false},
	}
	for _, c := range cases {
		r := dbReceipt(c.status, c.confirmed, c.bought)
		if got := s.eligible(r); got != c.want {
			t.Errorf("%s: eligible = %v, want %v", c.name, got, c.want)
		}
	}
}

// The real receipts from the first live batch: lines are parsed exactly as the server does, written to a
// database, and reconciled against a list made of what a person might type.
func TestRealReceiptsMatching(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 10, 10, 18, 0, 0, 0, ams)
	tested := 0
	for _, id := range []int{1, 2, 5, 6, 7, 13, 14, 20, 21, 23} {
		b, err := os.ReadFile(fmt.Sprintf("../receipt/testdata/live/receipt_%d.json", id))
		if err != nil {
			t.Fatal(err)
		}
		var res ocr.Result
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		parsed := receipt.Parse(res, receipt.Options{Location: ams, Now: now})
		var firstItem string
		for _, l := range parsed.Lines {
			if l.Kind == receipt.KindItem && len(l.RawText) > 5 {
				firstItem = l.RawText
				break
			}
		}
		if firstItem == "" {
			continue
		}
		tested++
		s, ctx := testService(t)
		s.now = func() time.Time { return time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC) }
		seedReceipt(t, s, 1, "2026-10-11T00:00:00Z", parsed.Lines)
		it, _, err := s.Add(ctx, Input{Text: firstItem})
		if err != nil {
			t.Fatal(err)
		}
		pepper, _, _ := s.Add(ctx, Input{Text: "zwarte peper gemalen"})
		res2, err := s.ReconcileReceipt(ctx, 1)
		if err != nil {
			t.Fatalf("receipt %d: %v", id, err)
		}
		got := false
		for _, c := range res2.Checked {
			if c.ID == it.ID {
				got = true
			}
			if c.ID == pepper.ID {
				t.Errorf("receipt %d: unrelated item %q checked", id, pepper.Text)
			}
		}
		if !got {
			t.Errorf("receipt %d: own line %q was not checked (checked %d, suggestions %d)", id, firstItem, len(res2.Checked), len(res2.Suggestions))
		}
		// Repeat: nothing more happens.
		if again, _ := s.ReconcileReceipt(ctx, 1); !again.Empty() {
			t.Errorf("receipt %d: second reconcile changed things: %+v", id, again)
		}
		// Undo puts it all back.
		back, err := s.UndoReceipt(ctx, 1)
		if err != nil || len(back) != len(res2.Checked) {
			t.Errorf("receipt %d: undo restored %d of %d (%v)", id, len(back), len(res2.Checked), err)
		}
	}
	if tested < 5 {
		t.Fatalf("only %d real receipts exercised", tested)
	}
}

func dbReceipt(status, confirmed, bought string) (r db.Receipt) {
	r.Status = status
	if confirmed != "" {
		r.ConfirmedAt = store.Str(confirmed)
	}
	if bought != "" {
		r.PurchasedAt = store.Str(bought)
	}
	return r
}

func seedReceipt(t *testing.T, s *Service, id int64, purchased string, lines []receipt.Line) {
	t.Helper()
	conf := store.TS(s.now())
	if _, err := s.conn.Exec(`INSERT INTO shops (id, name, created_at, updated_at) VALUES (1, 'AH', ?, ?)`, conf, conf); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.Exec(`INSERT INTO receipts (id, shop_id, purchased_at, image_path, image_sha256, status, confirmed_at, created_at, updated_at)
		VALUES (?, 1, ?, 'x', 'x', 'ok', ?, ?, ?)`, id, purchased, conf, conf, conf); err != nil {
		t.Fatal(err)
	}
	for i, l := range lines {
		if _, err := s.conn.Exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, i, l.RawText, match.Normalise(l.RawText), string(l.Kind), max(l.Qty, 1), "pc", l.LineTotalCents); err != nil {
			t.Fatal(err)
		}
	}
}
