package receipts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/jobs"
	"shop/internal/match"
	"shop/internal/ocr"
	"shop/internal/receipt"
	"shop/internal/store"
)

// Quantities of receipts read by an older parser are corrected from the stored OCR, for lines nobody
// edited, once.
func TestRequantifyOnce(t *testing.T) {
	ctx := context.Background()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	svc := New(conn, nil, nil, jobs.New(conn, log), Config{ImagesDir: t.TempDir(), Location: ams}, log)

	raw, err := os.ReadFile("../receipt/testdata/live/receipt_14.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := conn.Exec(`INSERT INTO receipts (image_path, image_sha256, status, raw_ocr_json, parser_version, confirmed_at, created_at, updated_at)
		VALUES ('p', 's', 'ok', ?, 2, '2026-10-01T00:00:00Z', 't', 't')`, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	ins := func(text string, total int64, edited int) int64 {
		r, err := conn.Exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, unit_price_cents, line_total_cents, user_edited)
			VALUES (?, 1, ?, ?, 'item', 1, 'pc', ?, ?, ?)`, id, text, text, total, total, edited)
		if err != nil {
			t.Fatal(err)
		}
		lid, _ := r.LastInsertId()
		return lid
	}
	// norm_text must equal what the parser produces now: take it from the parse
	parsed := receipt.Parse(unmarshalOCR(t, raw), receipt.Options{Location: ams, Now: time.Now()})
	var cola, other *receipt.Line
	for i := range parsed.Lines {
		l := &parsed.Lines[i]
		if l.Qty == 2 && cola == nil {
			cola = l
		} else if l.Qty == 1 && l.Kind == receipt.KindItem && other == nil {
			other = l
		}
	}
	if cola == nil || other == nil {
		t.Fatal("fixture has no counted line")
	}
	a := ins(cola.RawText, cola.LineTotalCents, 0)
	b := ins(other.RawText+" ", other.LineTotalCents, 0) // text not found again: left alone
	if _, err := conn.Exec(`UPDATE receipt_lines SET norm_text = ? WHERE id = ?`, normOf(cola.RawText), a); err != nil {
		t.Fatal(err)
	}
	ed := ins(cola.RawText, cola.LineTotalCents, 1) // edited: never touched
	if _, err := conn.Exec(`UPDATE receipt_lines SET norm_text = ? WHERE id = ?`, normOf(cola.RawText), ed); err != nil {
		t.Fatal(err)
	}

	n, err := svc.RequantifyOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d lines, want 1", n)
	}
	qty := func(lid int64) (q float64, up int64) {
		_ = conn.QueryRow(`SELECT qty, unit_price_cents FROM receipt_lines WHERE id = ?`, lid).Scan(&q, &up)
		return
	}
	if q, up := qty(a); q != 2 || up != cola.LineTotalCents/2 {
		t.Errorf("corrected line: qty %v price %d", q, up)
	}
	if q, _ := qty(b); q != 1 {
		t.Errorf("unmatched line changed: %v", q)
	}
	if q, _ := qty(ed); q != 1 {
		t.Errorf("edited line changed: %v", q)
	}
	if n, _ := svc.RequantifyOnce(ctx); n != 0 {
		t.Errorf("second run changed %d lines", n)
	}
}

func unmarshalOCR(t *testing.T, b []byte) ocr.Result {
	t.Helper()
	var r ocr.Result
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func normOf(s string) string { return match.Normalise(s) }
