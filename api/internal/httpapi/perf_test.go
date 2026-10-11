package httpapi_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"shop/internal/shopsuggest"
	"sort"
	"testing"
	"time"
)

// Latency of the hot endpoints on a large database. Opt in with PERF=1 (it seeds thousands of rows):
//
//	PERF=1 go test ./internal/httpapi -run TestPerf -v
func TestPerfEndpoints(t *testing.T) {
	if os.Getenv("PERF") == "" {
		t.Skip("set PERF=1")
	}
	e := newEnv(t)
	seedLarge(t, e, 2000)
	e.conn.Exec(`UPDATE receipts SET shop_id = NULL, confirmed_at = NULL WHERE id IN (1, 18, 35)`) // these carry an Albert Heijn header
	e.conn.Exec(`UPDATE receipts SET confirmed_at = NULL WHERE id % 4 = 0`)                        // a backlog of 500 unconfirmed receipts

	type probe struct{ name, method, path string }
	probes := []probe{
		{"receipts list", "GET", "/v1/receipts?limit=50"},
		{"receipt detail", "GET", "/v1/receipts/1500"},
		{"receipt counts", "GET", "/v1/receipts/counts"},
		{"shop suggestion", "GET", "/v1/receipts/1500/shop-suggestion"},
		{"shop suggestion (unassigned)", "GET", "/v1/receipts/1/shop-suggestion"},
		{"receipt detail (unassigned)", "GET", "/v1/receipts/18"},
		{"products list", "GET", "/v1/products?limit=50"},
		{"products search", "GET", "/v1/products?q=melk&limit=20"},
		{"products by category", "GET", "/v1/products?category_id=1&limit=50"},
		{"categories", "GET", "/v1/categories"},
		{"spend by month+category", "GET", "/v1/stats/spend?group_by=month&split_by=category&from=2025-01-01&to=2027-01-01"},
		{"spend by product", "GET", "/v1/stats/spend?group_by=product&from=2025-01-01&to=2027-01-01"},
		{"spend by shop", "GET", "/v1/stats/spend?group_by=shop&from=2025-01-01&to=2027-01-01"},
		{"price changes", "GET", "/v1/stats/price-changes"},
		{"frequent items", "GET", "/v1/stats/frequent-items"},
		{"product compare", "GET", "/v1/stats/products/10/compare"},
		{"search", "GET", "/v1/search?q=melk"},
		{"review queue (badge)", "GET", "/v1/products/review-queue?limit=0"},
		{"review queue (50)", "GET", "/v1/products/review-queue?limit=50"},
		{"shopping list", "GET", "/v1/list"},
		{"healthz", "GET", "/healthz"},
	}
	fmt.Printf("\n%-28s %8s %8s %8s\n", "endpoint (2000 receipts)", "p50 ms", "p95 ms", "max ms")
	for _, p := range probes {
		var ds []float64
		for i := 0; i < 7; i++ {
			start := time.Now()
			rec := e.do(p.method, p.path, nil)
			ds = append(ds, float64(time.Since(start).Microseconds())/1000)
			if rec.Code >= 500 {
				t.Fatalf("%s: %d %s", p.name, rec.Code, rec.Body)
			}
		}
		sort.Float64s(ds)
		fmt.Printf("%-28s %8.1f %8.1f %8.1f\n", p.name, ds[len(ds)/2], ds[len(ds)*95/100], ds[len(ds)-1])
	}
}

// seedLarge fills the database with n confirmed receipts, about 12 lines each, products and aliases.
func seedLarge(t *testing.T, e *env, n int) {
	t.Helper()
	files, _ := filepath.Glob("../receipt/testdata/live/receipt_*.json")
	var ocrs []string
	for _, f := range files {
		b, _ := os.ReadFile(f)
		ocrs = append(ocrs, string(b))
	}
	if len(ocrs) == 0 {
		t.Fatal("no OCR fixtures")
	}
	rng := rand.New(rand.NewSource(1))
	tx, err := e.conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range []string{"Albert Heijn", "Dirk", "Jumbo", "Lidl", "Plus"} {
		tx.Exec(`INSERT INTO shops (name, created_at, updated_at) VALUES (?, 't', 't')`, sh)
	}
	words := []string{"MELK", "KAAS", "BROOD", "KIP", "APPEL", "BANAAN", "SAP", "YOGHURT", "BOTER", "EIEREN", "PASTA", "RIJST", "TOMAAT", "UI", "KOFFIE", "THEE", "COLA", "BIER", "WIJN", "ZEEP"}
	prods := 3000
	for i := 0; i < prods; i++ {
		name := fmt.Sprintf("%s %s %d", words[rng.Intn(len(words))], words[rng.Intn(len(words))], i)
		tx.Exec(`INSERT INTO products (name, category_id, created_at, updated_at) VALUES (?, ?, 't', 't')`, name, 1+rng.Intn(10))
	}
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * 7 * time.Hour).Format("2006-01-02T15:04:05Z")
		res, _ := tx.Exec(`INSERT INTO receipts (shop_id, purchased_at, total_cents, parsed_sum_cents, image_path, image_sha256, status, raw_ocr_json, confirmed_at, created_at, updated_at)
			VALUES (?, ?, 5000, 5000, ?, ?, 'ok', ?, ?, ?, ?)`, 1+rng.Intn(5), at, fmt.Sprint("p", i), fmt.Sprint("s", i), ocrs[i%len(ocrs)], at, at, at)
		rid, _ := res.LastInsertId()
		for l := 0; l < 12; l++ {
			pid := 1 + rng.Intn(prods)
			raw := fmt.Sprintf("%s %d", words[rng.Intn(len(words))], l)
			tx.Exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents, product_id)
				VALUES (?, ?, ?, ?, 'item', 1, 'pc', ?, ?)`, rid, l, raw, raw, 100+rng.Intn(900), pid)
		}
	}
	for i := 0; i < 6000; i++ {
		tx.Exec(`INSERT OR IGNORE INTO product_aliases (shop_id, norm_text, product_id, created_at) VALUES (?, ?, ?, 't')`, 1+rng.Intn(5), fmt.Sprintf("alias %d", i), 1+rng.Intn(prods))
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := shopsuggest.NewService(e.conn).Reindex(context.Background()); err != nil { // as the server does after a start
		t.Fatal(err)
	}
}

// While background work writes (processing and linking receipts), reads and small writes from the app must
// stay fast. PERF=1 go test ./internal/httpapi -run TestPerfWhileJobsRun -v
func TestPerfWhileJobsRun(t *testing.T) {
	if os.Getenv("PERF") == "" {
		t.Skip("set PERF=1")
	}
	e := newEnv(t)
	seedLarge(t, e, 2000)
	// 150 fresh receipts whose lines have no product yet: confirming links or creates products
	tx, _ := e.conn.Begin()
	for i := 0; i < 150; i++ {
		res, _ := tx.Exec(`INSERT INTO receipts (shop_id, purchased_at, total_cents, parsed_sum_cents, image_path, image_sha256, status, confirmed_at, created_at, updated_at)
			VALUES (1, '2026-09-01T10:00:00Z', 5000, 5000, ?, ?, 'ok', '2026-09-01T10:00:00Z', 't', 't')`, fmt.Sprint("n", i), fmt.Sprint("ns", i))
		rid, _ := res.LastInsertId()
		for l := 0; l < 15; l++ {
			raw := fmt.Sprintf("NIEUW PRODUCT %d-%d", i, l)
			tx.Exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents) VALUES (?, ?, ?, ?, 'item', 1, 'pc', 199)`, rid, l, raw, raw)
		}
	}
	tx.Commit()
	var ids []int64
	rows, _ := e.conn.Query(`SELECT id FROM receipts WHERE image_path LIKE 'n%'`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()

	done := make(chan time.Duration)
	go func() {
		start := time.Now()
		for _, id := range ids {
			if _, err := e.auto.ProcessReceipt(context.Background(), id, "backfill"); err != nil {
				t.Error(err)
			}
		}
		done <- time.Since(start)
	}()
	var reads, writes []float64
	var jobTime time.Duration
loop:
	for i := 0; ; i++ {
		select {
		case jobTime = <-done:
			break loop
		default:
		}
		st := time.Now()
		e.do("GET", "/v1/products?limit=50", nil)
		reads = append(reads, float64(time.Since(st).Microseconds())/1000)
		st = time.Now()
		e.do("POST", "/v1/list", map[string]any{"text": fmt.Sprint("item ", i), "client_id": fmt.Sprint("c", i)})
		writes = append(writes, float64(time.Since(st).Microseconds())/1000)
		time.Sleep(20 * time.Millisecond)
	}
	pct := func(v []float64, p int) float64 { sort.Float64s(v); return v[len(v)*p/100] }
	fmt.Printf("\nauto-products over 150 receipts (2250 new lines) took %v\n", jobTime.Round(time.Millisecond))
	fmt.Printf("read  GET /v1/products   n=%d  p50 %.1f ms  p95 %.1f ms  max %.1f ms\n", len(reads), pct(reads, 50), pct(reads, 95), reads[len(reads)-1])
	fmt.Printf("write POST /v1/list      n=%d  p50 %.1f ms  p95 %.1f ms  max %.1f ms\n", len(writes), pct(writes, 50), pct(writes, 95), writes[len(writes)-1])
}
