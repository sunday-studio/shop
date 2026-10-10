package autoprod

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"shop/internal/match"
	"shop/internal/store"
)

type fixLine struct {
	Raw   string `json:"raw"`
	Kind  string `json:"kind"`
	Cents int64  `json:"cents"`
}
type fixReceipt struct {
	File  string    `json:"file"`
	Shop  string    `json:"shop"`
	Date  string    `json:"date"`
	Lines []fixLine `json:"lines"`
}

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := store.Migrate(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func newService(t *testing.T, conn *sql.DB, enabled bool) *Service {
	t.Helper()
	return New(conn, match.NewMatcher(conn), nil, nil, nil, slog.New(slog.DiscardHandler), enabled)
}

// loadReceipts inserts the 13 real receipts (confirmed, in date order) and returns their ids in that order.
func loadReceipts(t *testing.T, conn *sql.DB) []int64 {
	t.Helper()
	b, err := os.ReadFile("testdata/receipts.json")
	if err != nil {
		t.Fatal(err)
	}
	var recs []fixReceipt
	if err := json.Unmarshal(b, &recs); err != nil {
		t.Fatal(err)
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Date < recs[j].Date })
	shops := map[string]int64{}
	var ids []int64
	for _, r := range recs {
		sid, ok := shops[r.Shop]
		if !ok {
			res, err := conn.Exec(`INSERT INTO shops (name, created_at, updated_at) VALUES (?, 't', 't')`, r.Shop)
			if err != nil {
				t.Fatal(err)
			}
			sid, _ = res.LastInsertId()
			shops[r.Shop] = sid
		}
		var date any
		if r.Date != "" {
			date = r.Date
		}
		res, err := conn.Exec(`INSERT INTO receipts (shop_id, purchased_at, image_path, image_sha256, status, confirmed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'ok', '2026-10-10T00:00:00Z', '2026-10-10T00:00:00Z', 't')`, sid, date, "p"+r.File, "s"+r.File)
		if err != nil {
			t.Fatal(err)
		}
		rid, _ := res.LastInsertId()
		ids = append(ids, rid)
		for i, l := range r.Lines {
			if _, err := conn.Exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, line_total_cents) VALUES (?, ?, ?, ?, ?, ?)`,
				rid, i, l.Raw, match.Normalise(l.Raw), l.Kind, l.Cents); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}

func count(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
