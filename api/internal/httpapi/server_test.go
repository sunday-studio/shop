package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shop/internal/apitypes"
	"shop/internal/auth"
	"shop/internal/autoprod"
	"shop/internal/english"
	"shop/internal/httpapi"
	"shop/internal/jobs"
	"shop/internal/logos"
	"shop/internal/match"
	"shop/internal/mcpserver"
	"shop/internal/ocr"
	"shop/internal/openfoodfacts"
	"shop/internal/products"
	"shop/internal/receipts"
	"shop/internal/search"
	"shop/internal/shopping"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/store/db"
)

// env is a full in-process stack: SQLite, fake OCR sidecar, job queue, API.
type env struct {
	auto    *autoprod.Service
	t       *testing.T
	h       http.Handler
	queue   *jobs.Queue
	token   string
	dataDir string
	ocr     *fakeOCR
	q       *db.Queries
	conn    *sql.DB
}

type fakeOCR struct {
	srv   *httptest.Server
	ready atomic.Bool
	fail  atomic.Bool
	rows  atomic.Value // []string, "left|right"
	calls atomic.Int32
}

func newFakeOCR() *fakeOCR {
	f := &fakeOCR{}
	f.ready.Store(true)
	f.rows.Store([]string{"01-10-2026 10:00|", "MELK|1,29", "TOTAAL|1,29"})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_ = json.NewEncoder(w).Encode(ocr.Health{Engine: "fake", Ready: f.ready.Load()})
			return
		}
		f.calls.Add(1)
		if f.fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		res := ocr.Result{Engine: "fake", MS: 5}
		for i, row := range f.rows.Load().([]string) {
			left, right, _ := strings.Cut(row, "|")
			y := float64(40 + i*40)
			if left != "" {
				res.Lines = append(res.Lines, ocr.Line{Text: left, Conf: 0.99, Box: [4]float64{10, y, 200, y + 25}})
			}
			if right != "" {
				res.Lines = append(res.Lines, ocr.Line{Text: right, Conf: 0.99, Box: [4]float64{300, y, 360, y + 25}})
			}
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	return f
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	conn, err := store.Open(filepath.Join(dir, "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := store.Migrate(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	f := newFakeOCR()
	t.Cleanup(f.srv.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	queue := jobs.New(conn, log)
	queue.Backoff = func(int64) time.Duration { return 0 } // retries are due immediately
	ocrClient := ocr.New(f.srv.URL)
	svc := receipts.New(conn, ocrClient, nil, queue, receipts.Config{ImagesDir: filepath.Join(dir, "images"), Location: ams}, log)
	foodFacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/product/8710400000017.json" {
			_, _ = w.Write([]byte(`{"status":1,"product":{"product_name_nl":"Halfvolle melk","brands":"AH",
				"product_quantity":1500,"product_quantity_unit":"ml",
				"nutriments":{"energy-kcal_100g":46,"fat_100g":1.5,"proteins_100g":3.5}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	t.Cleanup(foodFacts.Close)
	prods := products.New(conn, match.NewMatcher(conn), openfoodfacts.New("test").WithBaseURL(foodFacts.URL),
		filepath.Join(dir, "images"), log)
	st := stats.New(conn, ams)
	list := shopping.New(conn, match.NewMatcher(conn), st)
	logoDev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nologo.nl" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNG:" + strings.TrimPrefix(r.URL.Path, "/")))
	}))
	t.Cleanup(logoDev.Close)
	eng := english.New(conn)
	if err := eng.Load(context.Background()); err != nil { // resets the global user glossary per test
		t.Fatal(err)
	}
	foodFactsClient := openfoodfacts.New("test").WithBaseURL(foodFacts.URL)
	auto := autoprod.New(conn, match.NewMatcher(conn), prods, foodFactsClient, queue, log, true)
	auto.RegisterJobs(queue)
	mcpSrv := mcpserver.New(mcpserver.Deps{DB: conn, Receipts: svc, Products: prods, Stats: st, List: list,
		Matcher: match.NewMatcher(conn), English: eng, Location: ams, Auto: auto})
	api := httpapi.New(httpapi.Deps{DB: conn, OCR: ocrClient, Receipts: svc, Products: prods, Stats: st, List: list, English: eng, Auto: auto,
		Search:   search.New(conn, prods, match.NewMatcher(conn)),
		Logos:    logos.New("pk_test", filepath.Join(dir, "images")).WithBaseURL(logoDev.URL),
		Location: ams, Log: log, DataDir: dir, Authed: map[string]http.Handler{"/mcp": mcpserver.Handler(mcpSrv)}})

	q := db.New(conn)
	tok, err := auth.Create(context.Background(), q, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, h: api.Handler(), queue: queue, token: tok, dataDir: dir, ocr: f, q: q, conn: conn, auto: auto}
}

func (e *env) do(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+e.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) upload(shopID int64, img []byte, extra map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if shopID != 0 { // 0 = no shop: an unassigned upload
		_ = mw.WriteField("shop_id", fmt.Sprint(shopID))
	}
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("image", "r.png")
	_, _ = fw.Write(img)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/receipts", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// drain runs queued jobs synchronously.
func (e *env) drain() {
	for e.queue.RunOne(context.Background()) {
	}
}

func decodeAs[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v (%d %s)", v, err, rec.Code, rec.Body)
	}
	return v
}

func pngBytes(seed uint8) []byte {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.Gray{Y: seed})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func (e *env) shop(name string) apitypes.Shop {
	e.t.Helper()
	rec := e.do("POST", "/v1/shops", map[string]any{"name": name, "city": "Amsterdam"})
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create shop: %d %s", rec.Code, rec.Body)
	}
	return decodeAs[apitypes.Shop](e.t, rec)
}

// ---------------------------------------------------------------- tests

func TestHealth(t *testing.T) {
	e := newEnv(t)
	get := func() (int, apitypes.Health) {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
		return rec.Code, decodeAs[apitypes.Health](t, rec)
	}
	if code, h := get(); code != 200 || h.Status != apitypes.HealthStatusOk || !h.Checks["ocr"].Ok {
		t.Fatalf("healthy: %d %+v", code, h)
	}
	e.ocr.ready.Store(false)
	if code, h := get(); code != 503 || h.Status != apitypes.HealthStatusDegraded {
		t.Fatalf("loading: %d %+v", code, h)
	}
	e.ocr.srv.Close()
	if code, h := get(); code != 503 || h.Checks["ocr"].Ok {
		t.Fatalf("down: %d %+v", code, h)
	}
}

func TestVersionIsPublic(t *testing.T) {
	e := newEnv(t)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/version", nil))
	if v := decodeAs[apitypes.Version](t, rec); rec.Code != 200 || v.SchemaVersion < 3 {
		t.Fatalf("got %d %+v", rec.Code, v)
	}
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	for _, hdr := range []string{"", "Bearer nope", "Bearer pt_wrong"} {
		req := httptest.NewRequest("GET", "/v1/shops", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%q: got %d", hdr, rec.Code)
		}
	}
	if rec := e.do("GET", "/v1/shops", nil); rec.Code != 200 {
		t.Fatalf("valid token: %d", rec.Code)
	}
}

func TestShopsCRUD(t *testing.T) {
	e := newEnv(t)
	s := e.shop("Lidl")
	if s.City == nil || *s.City != "Amsterdam" {
		t.Errorf("city = %v", s.City)
	}
	if rec := e.do("POST", "/v1/shops", map[string]any{"name": "  "}); rec.Code != 400 {
		t.Errorf("blank name: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/shops", map[string]any{"name": "X", "bogus": 1}); rec.Code != 400 {
		t.Errorf("unknown field: %d", rec.Code)
	}
	rec := e.do("PATCH", fmt.Sprintf("/v1/shops/%d", s.Id), map[string]any{"name": "Lidl Zuid"})
	if up := decodeAs[apitypes.Shop](t, rec); up.Name != "Lidl Zuid" || up.City != nil {
		t.Errorf("update: %+v", up)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/shops/%d", s.Id), nil); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := e.do("GET", fmt.Sprintf("/v1/shops/%d", s.Id), nil); rec.Code != 404 {
		t.Errorf("get deleted: %d", rec.Code)
	}
	if list := decodeAs[[]apitypes.Shop](t, e.do("GET", "/v1/shops", nil)); len(list) != 0 {
		t.Errorf("list after delete: %d", len(list))
	}
}

func TestCategories(t *testing.T) {
	e := newEnv(t)
	cats := decodeAs[[]apitypes.Category](t, e.do("GET", "/v1/categories", nil))
	if len(cats) != 10 {
		t.Fatalf("seeded = %d", len(cats))
	}
	if rec := e.do("POST", "/v1/categories", map[string]any{"name": "Dairy"}); rec.Code != 409 {
		t.Errorf("duplicate: %d", rec.Code)
	}
	c := decodeAs[apitypes.Category](t, e.do("POST", "/v1/categories", map[string]any{"name": "Baby", "sort": 95}))
	rec := e.do("PATCH", fmt.Sprintf("/v1/categories/%d", c.Id), map[string]any{"name": "Baby & kind"})
	if up := decodeAs[apitypes.Category](t, rec); up.Name != "Baby & kind" || up.Sort != 0 { // sort is accepted and ignored
		t.Errorf("update: %+v", up)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d", c.Id), nil); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestReceiptLifecycle(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.ocr.rows.Store([]string{
		"ALBERT HEIJN|", "03-10-2026 14:32|",
		"MELK|1,29", "BANANEN|1,78", "2 x 0,89|", "BONUS BANANEN|-0,50",
		"TOTAAL|2,57", "PIN|2,57",
	})

	img := pngBytes(1)
	rec := e.upload(shop.Id, img, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	r := decodeAs[apitypes.Receipt](t, rec)
	if r.Status != apitypes.ReceiptStatusPending {
		t.Fatalf("status = %s", r.Status)
	}

	// Same bytes again → 409 with the original id.
	if dup := e.upload(shop.Id, img, nil); dup.Code != 409 || !strings.Contains(dup.Body.String(), fmt.Sprint(r.Id)) {
		t.Fatalf("duplicate: %d %s", dup.Code, dup.Body)
	}
	if bad := e.upload(shop.Id, []byte("not an image"), nil); bad.Code != 415 {
		t.Fatalf("unsupported: %d", bad.Code)
	}
	if bad := e.upload(9999, pngBytes(2), nil); bad.Code != 400 {
		t.Fatalf("unknown shop: %d", bad.Code)
	}

	e.drain()
	path := fmt.Sprintf("/v1/receipts/%d", r.Id)
	got := decodeAs[apitypes.Receipt](t, e.do("GET", path, nil))
	if got.Status != apitypes.ReceiptStatusOk || got.TotalCents == nil || *got.TotalCents != 257 {
		t.Fatalf("processed: status=%s total=%v reasons=%v", got.Status, got.TotalCents, got.ReviewReasons)
	}
	if got.PurchasedAt == nil || got.PurchasedAt.UTC().Format(time.RFC3339) != "2026-10-03T12:32:00Z" {
		t.Errorf("purchased_at = %v", got.PurchasedAt)
	}
	lines := *got.Lines
	if len(lines) != 3 || lines[1].Qty != 2 || lines[2].ParentLineId == nil || *lines[2].ParentLineId != lines[1].Id {
		t.Fatalf("lines = %+v", lines)
	}

	// Image is served.
	if img := e.do("GET", path+"/image", nil); img.Code != 200 || img.Header().Get("Content-Type") != "image/png" {
		t.Errorf("image: %d %s", img.Code, img.Header().Get("Content-Type"))
	}

	// Editing a line marks it, flips review status on mismatch.
	lineRec := e.do("PATCH", fmt.Sprintf("/v1/lines/%d", lines[0].Id), map[string]any{
		"raw_text": "MELK HALFVOL", "kind": "item", "line_total_cents": 139, "unit_price_cents": 139,
	})
	if l := decodeAs[apitypes.ReceiptLine](t, lineRec); !l.UserEdited || l.RawText != "MELK HALFVOL" {
		t.Fatalf("edited line: %+v", l)
	}
	got = decodeAs[apitypes.Receipt](t, e.do("GET", path, nil))
	if got.Status != apitypes.ReceiptStatusNeedsReview || *got.ParsedSumCents != 267 {
		t.Fatalf("after edit: %s sum=%v reasons=%v", got.Status, *got.ParsedSumCents, got.ReviewReasons)
	}

	// Fix the total via PATCH → ok again.
	got = decodeAs[apitypes.Receipt](t, e.do("PATCH", path, map[string]any{"total_cents": 267}))
	if got.Status != apitypes.ReceiptStatusOk || len(got.ReviewReasons) != 0 {
		t.Fatalf("after total fix: %s %v", got.Status, got.ReviewReasons)
	}

	// Reparse is refused once edited, allowed with force.
	if rec := e.do("POST", path+"/reparse", nil); rec.Code != 409 {
		t.Fatalf("reparse edited: %d", rec.Code)
	}
	calls := e.ocr.calls.Load()
	if rec := e.do("POST", path+"/reparse?force=true", nil); rec.Code != 202 {
		t.Fatalf("forced reparse: %d %s", rec.Code, rec.Body)
	}
	e.drain()
	if e.ocr.calls.Load() != calls {
		t.Errorf("reparse must not call OCR")
	}
	got = decodeAs[apitypes.Receipt](t, e.do("GET", path, nil))
	if (*got.Lines)[0].RawText != "MELK" || *got.TotalCents != 257 {
		t.Errorf("after forced reparse: %+v total=%d", (*got.Lines)[0], *got.TotalCents)
	}

	// Add + delete a manual line.
	added := decodeAs[apitypes.ReceiptLine](t, e.do("POST", path+"/lines", map[string]any{
		"raw_text": "STATIEGELD", "kind": "deposit", "line_total_cents": 15,
	}))
	if added.LineNo != 4 || added.Qty != 1 || added.Unit != "pc" {
		t.Errorf("added: %+v", added)
	}
	if rec := e.do("POST", path+"/lines", map[string]any{"raw_text": "x", "kind": "weird", "line_total_cents": 1}); rec.Code != 400 {
		t.Errorf("bad kind: %d", rec.Code)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/lines/%d", added.Id), nil); rec.Code != 204 {
		t.Errorf("delete line: %d", rec.Code)
	}

	// Confirm.
	got = decodeAs[apitypes.Receipt](t, e.do("POST", path+"/confirm", nil))
	if got.ConfirmedAt == nil || got.Status != apitypes.ReceiptStatusOk {
		t.Errorf("confirm: %+v", got)
	}

	// Delete removes the image file.
	imgPath := filepath.Join(e.dataDir, "images")
	if rec := e.do("DELETE", path, nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	left := 0
	_ = filepath.Walk(imgPath, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			left++
		}
		return nil
	})
	if left != 0 {
		t.Errorf("%d image files left after delete", left)
	}
	if rec := e.do("GET", path, nil); rec.Code != 404 {
		t.Errorf("get deleted: %d", rec.Code)
	}
}

func TestUploadDateOverrideSurvivesParse(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("Jumbo")
	rec := e.upload(shop.Id, pngBytes(3), map[string]string{"purchased_at": "2026-09-01T09:00:00Z"})
	r := decodeAs[apitypes.Receipt](t, rec)
	e.drain()
	got := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil))
	if got.PurchasedAt == nil || !got.PurchasedAt.Equal(time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("purchased_at = %v, want upload override", got.PurchasedAt)
	}
}

func TestOCRFailureRetriesThenFails(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("Plus")
	e.ocr.fail.Store(true)
	r := decodeAs[apitypes.Receipt](t, e.upload(shop.Id, pngBytes(4), nil))

	e.drain() // all attempts fail (zero backoff)
	if n := e.ocr.calls.Load(); n != jobs.MaxAttempts {
		t.Errorf("ocr calls = %d, want %d", n, jobs.MaxAttempts)
	}
	got := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil))
	if got.Status != apitypes.ReceiptStatusFailed {
		t.Fatalf("after retries: %s", got.Status)
	}

	// Re-OCR after the sidecar recovers.
	e.ocr.fail.Store(false)
	if rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/reocr", r.Id), nil); rec.Code != 202 {
		t.Fatalf("reocr: %d %s", rec.Code, rec.Body)
	}
	e.drain()
	got = decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil))
	if got.Status != apitypes.ReceiptStatusOk {
		t.Fatalf("after reocr: %s %v", got.Status, got.ReviewReasons)
	}
}

func TestListReceipts(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("A"), e.shop("B")
	for i := range 5 {
		shop := a
		if i%2 == 1 {
			shop = b
		}
		e.ocr.rows.Store([]string{fmt.Sprintf("0%d-10-2026|", i+1), "MELK|1,29", "TOTAAL|1,29"})
		if rec := e.upload(shop.Id, pngBytes(uint8(10+i)), nil); rec.Code != 202 {
			t.Fatal(rec.Body)
		}
		e.drain()
	}
	page := decodeAs[apitypes.ReceiptPage](t, e.do("GET", "/v1/receipts?limit=2", nil))
	if len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("page 1: %d %v", len(page.Items), page.NextCursor)
	}
	page2 := decodeAs[apitypes.ReceiptPage](t, e.do("GET", "/v1/receipts?limit=2&cursor="+*page.NextCursor, nil))
	if len(page2.Items) != 2 || page2.Items[0].Id >= page.Items[1].Id {
		t.Fatalf("page 2: %+v", page2.Items)
	}
	byShop := decodeAs[apitypes.ReceiptPage](t, e.do("GET", fmt.Sprintf("/v1/receipts?shop_id=%d", b.Id), nil))
	if len(byShop.Items) != 2 {
		t.Errorf("by shop: %d", len(byShop.Items))
	}
	byDate := decodeAs[apitypes.ReceiptPage](t, e.do("GET", "/v1/receipts?from=2026-10-02&to=2026-10-03", nil))
	if len(byDate.Items) != 2 {
		t.Errorf("by date: %d", len(byDate.Items))
	}
	if rec := e.do("GET", "/v1/receipts?from=10/2026", nil); rec.Code != 400 {
		t.Errorf("bad date: %d", rec.Code)
	}
}

func TestHealthReportsBackups(t *testing.T) {
	e := newEnv(t)
	health := func() (int, apitypes.Health) {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
		return rec.Code, decodeAs[apitypes.Health](t, rec)
	}
	// No backup has ever run: no backup check, still healthy.
	if code, h := health(); code != 200 || h.Checks["storage"].Detail == nil {
		t.Fatalf("no backups yet: %d %+v", code, h.Checks)
	} else if _, ok := h.Checks["backup"]; ok {
		t.Fatal("backup check should be absent before the first run")
	}
	write := func(st string) {
		if err := os.WriteFile(filepath.Join(e.dataDir, "backup_status.json"), []byte(st), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(fmt.Sprintf(`{"ok":true,"at":%q,"snapshot":"shop-x.db"}`, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339)))
	if code, h := health(); code != 200 || !h.Checks["backup"].Ok {
		t.Errorf("fresh backup: %d %+v", code, h.Checks["backup"])
	}
	write(fmt.Sprintf(`{"ok":true,"at":%q}`, time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339)))
	if code, h := health(); code != 503 || h.Checks["backup"].Ok || !strings.Contains(*h.Checks["backup"].Detail, "stale") {
		t.Errorf("stale backup: %d %+v", code, h.Checks["backup"])
	}
	write(fmt.Sprintf(`{"ok":false,"at":%q,"error":"backup directory not available"}`, time.Now().UTC().Format(time.RFC3339)))
	if code, h := health(); code != 503 || !strings.Contains(*h.Checks["backup"].Detail, "not available") {
		t.Errorf("failed backup: %d %+v", code, h.Checks["backup"])
	}
}

func TestUploadRejectsHEIC(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("Jumbo")
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 64)...)
	rec := e.upload(shop.Id, heic, nil)
	if rec.Code != 415 || !strings.Contains(rec.Body.String(), "HEIC") {
		t.Fatalf("heic upload: %d %s; want 415 mentioning HEIC", rec.Code, rec.Body)
	}
}
