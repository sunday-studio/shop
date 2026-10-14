package tenantapp_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/auth"
	"shop/internal/config"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/tenancy"
	"shop/internal/tenantapp"
)

type fixture struct {
	rt    *tenantapp.Runtime
	multi *tenancy.Multi
	dir   *tenancy.MemoryDirectory
	h     http.Handler
	ocr   *httptest.Server
	rows  []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	f.ocr = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_ = json.NewEncoder(w).Encode(map[string]any{"engine": "fake", "ready": true})
			return
		}
		lines := []map[string]any{}
		for i, row := range f.rows {
			left, right, _ := strings.Cut(row, "|")
			y := float64(40 + i*40)
			if left != "" {
				lines = append(lines, map[string]any{"text": left, "conf": 0.99, "box": []float64{10, y, 200, y + 25}})
			}
			if right != "" {
				lines = append(lines, map[string]any{"text": right, "conf": 0.99, "box": []float64{300, y, 360, y + 25}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"engine": "fake", "ms": 5, "lines": lines})
	}))
	t.Cleanup(f.ocr.Close)
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	cfg := config.Config{DataDir: t.TempDir(), OCRURL: f.ocr.URL, FoodFactsUserAgent: "t", FoodFactsBaseURL: "http://127.0.0.1:1", AutoProducts: true, Location: ams, Workers: 1, BackupKeepDays: 30}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.rt = tenantapp.NewRuntime(cfg, log)
	f.rt.Backoff = func(int64) time.Duration { return 0 }
	var err error
	f.multi, err = tenancy.NewMulti(tenancy.MultiConfig{Root: t.TempDir(), BackupRoot: t.TempDir(), MaxOpen: 4, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.multi.Close() })
	f.dir = tenancy.NewMemoryDirectory()
	f.h = f.rt.Gateway(f.multi, tenancy.DirectoryResolver{Dir: f.dir}).Handler()
	return f
}

// household provisions id and returns a token for it.
func (f *fixture) household(t *testing.T, id string) string {
	t.Helper()
	ctx := context.Background()
	if err := f.multi.Provision(ctx, id); err != nil {
		t.Fatal(err)
	}
	tn, release, err := f.multi.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tok, err := auth.Create(ctx, db.New(tn.DB), "t")
	if err != nil {
		t.Fatal(err)
	}
	f.dir.Register(auth.Hash(tok), id)
	return tok
}

func (f *fixture) do(tok, method, path string, body io.Reader, ct string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func pngBytes(seed uint8) []byte {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.Gray{Y: seed})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func (f *fixture) upload(t *testing.T, tok string, seed uint8) int64 {
	t.Helper()
	var b bytes.Buffer
	mw := newMultipart(&b)
	fw, _ := mw.CreateFormFile("image", "r.png")
	_, _ = fw.Write(pngBytes(seed))
	_ = mw.Close()
	rec := f.do(tok, "POST", "/v1/receipts", &b, mw.FormDataContentType())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var out struct{ ID int64 }
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	out.ID = int64(m["id"].(float64))
	return out.ID
}

func TestGatewayMultiMode(t *testing.T) {
	f := newFixture(t)
	a := f.household(t, "house-a")
	b := f.household(t, "house-b")

	// Public routes answer without a token and carry no household data.
	for path, want := range map[string]string{"/healthz": `"status":"ok"`, "/v1/server-info": `"mode":"hosted"`, "/v1/auth/status": `"pairing_enabled":false`} {
		rec := f.do("", "GET", path, nil, "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := f.do("", "POST", "/v1/auth/pair", strings.NewReader(`{"password":"x"}`), "application/json"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "login_method_unavailable") {
		t.Errorf("pair in multi mode: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("", "GET", "/version", nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"schema_version":`+itoa(store.LatestVersion())) {
		t.Errorf("/version: %d %s", rec.Code, rec.Body)
	}

	// Tokens: missing, unknown, and each household's own.
	if rec := f.do("", "GET", "/v1/shops", nil, ""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := f.do("st_unknown", "GET", "/v1/shops", nil, ""); rec.Code != 401 {
		t.Errorf("unknown token: %d", rec.Code)
	}
	f.do(a, "POST", "/v1/shops", strings.NewReader(`{"name":"Only A"}`), "application/json")
	if rec := f.do(a, "GET", "/v1/shops", nil, ""); !strings.Contains(rec.Body.String(), "Only A") {
		t.Errorf("A: %s", rec.Body)
	}
	if rec := f.do(b, "GET", "/v1/shops", nil, ""); rec.Code != 200 || strings.Contains(rec.Body.String(), "Only A") {
		t.Errorf("B sees A's shop: %s", rec.Body)
	}
	// MCP is reached through the same token resolution.
	if rec := f.do("st_unknown", "POST", "/mcp", strings.NewReader(`{}`), "application/json"); rec.Code != 401 {
		t.Errorf("mcp unknown token: %d", rec.Code)
	}
}

func TestGatewayBrokenHouseholdAnswers503AndOthersWork(t *testing.T) {
	f := newFixture(t)
	good := f.household(t, "good")
	bad := f.household(t, "bad")
	f.multi.Close() // reopen from disk below
	_ = os.WriteFile(filepath.Join(f.multi.Dir("bad"), "shop.db"), []byte("not a database, just padding padding padding padding padding"), 0o644)
	m2, err := tenancy.NewMulti(tenancy.MultiConfig{Root: filepath.Dir(filepath.Dir(f.multi.Dir("bad"))), MaxOpen: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	f.h = f.rt.Gateway(m2, tenancy.DirectoryResolver{Dir: f.dir}).Handler()
	rec := f.do(bad, "GET", "/v1/shops", nil, "")
	if rec.Code != 503 || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "household_unavailable") {
		t.Errorf("bad household: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(good, "GET", "/v1/shops", nil, ""); rec.Code != 200 {
		t.Errorf("good household: %d %s", rec.Code, rec.Body)
	}
}

// The fair scheduler runs each household's receipt reading with the household's own service graph.
func TestSchedulerReadsReceiptsOfEveryHousehold(t *testing.T) {
	f := newFixture(t)
	a := f.household(t, "house-a")
	b := f.household(t, "house-b")
	sched := f.rt.Scheduler(f.multi, 2)
	sched.Sweep = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sched.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	f.rows = []string{"01-09-2026 10:00|", "MELK|1,29", "TOTAAL|1,29"}
	ra := f.upload(t, a, 1)
	rb := f.upload(t, b, 2)
	waitStatus := func(tok string, id int64) string {
		for range 300 {
			rec := f.do(tok, "GET", "/v1/receipts/"+itoa(id), nil, "")
			var m map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &m)
			if s, _ := m["status"].(string); s != "pending" && s != "processing" && s != "" {
				return s
			}
			time.Sleep(20 * time.Millisecond)
		}
		return "timeout"
	}
	if s := waitStatus(a, ra); s != "ok" {
		t.Errorf("A's receipt: %s", s)
	}
	if s := waitStatus(b, rb); s != "ok" {
		t.Errorf("B's receipt: %s", s)
	}
}

func TestBackupAndExportAreScopedToOneHousehold(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.household(t, "house-a")
	f.household(t, "house-b")
	f.do(a, "POST", "/v1/shops", strings.NewReader(`{"name":"Only A"}`), "application/json")
	f.rows = []string{"01-09-2026 10:00|", "MELK|1,29", "TOTAAL|1,29"}
	f.upload(t, a, 7)

	res, err := f.rt.BackupAll(ctx, f.multi)
	if err != nil || len(res) != 2 || !res["house-a"].OK || !res["house-b"].OK {
		t.Fatalf("backup: %v %+v", err, res)
	}
	for _, id := range []string{"house-a", "house-b"} {
		snaps, _ := os.ReadDir(filepath.Join(f.multi.BackupDir(id), "db"))
		if len(snaps) != 1 {
			t.Errorf("%s: %d snapshots", id, len(snaps))
		}
	}
	if a := res["house-a"]; a.ImagesTotal != 1 || a.ImagesCopied != 1 {
		t.Errorf("A's images: %+v", a)
	}
	if b := res["house-b"]; b.ImagesTotal != 0 {
		t.Errorf("B has images in its backup: %+v", b)
	}

	// Export of A: a zip with A's database and A's images only.
	tn, release, _ := f.multi.Acquire(ctx, "house-a")
	var buf bytes.Buffer
	if err := tenantapp.Export(ctx, tn, &buf); err != nil {
		t.Fatal(err)
	}
	release()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	if files["manifest.json"] == nil || files["shop.db"] == nil {
		t.Fatalf("archive: %v", files)
	}
	imgs := 0
	for n := range files {
		if strings.HasPrefix(n, "images/") {
			imgs++
		}
	}
	if imgs != 1 {
		t.Errorf("%d images in the archive, want 1", imgs)
	}
	rc, _ := files["shop.db"].Open()
	dbBytes, _ := io.ReadAll(rc)
	rc.Close()
	p := filepath.Join(t.TempDir(), "x.db")
	_ = os.WriteFile(p, dbBytes, 0o644)
	conn, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM shops WHERE name = 'Only A'`).Scan(&n)
	if n != 1 {
		t.Errorf("exported database lacks A's shop")
	}
	assertNoTemp(t, tn.Dir)
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "export-") {
			t.Errorf("export left %s behind", e.Name())
		}
	}
}

var _ = sql.ErrNoRows

// Single mode runs through the same gateway: the pairing flow, the token check and the public routes
// behave as a stand-alone server.
func TestGatewaySingleModeIsTransparent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dir := t.TempDir()
	conn, err := tenancy.OpenDB(ctx, filepath.Join(dir, "shop.db"), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	prov := tenancy.NewSingle(conn, dir, filepath.Join(dir, "images"))
	defer prov.Close()
	if err := auth.SetPassword(ctx, db.New(conn), "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	f.h = f.rt.Gateway(prov, tenancy.SingleResolver{}).Handler()

	if rec := f.do("", "GET", "/v1/server-info", nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mode":"single"`) || !strings.Contains(rec.Body.String(), `"login_methods":["pairing_password"]`) {
		t.Errorf("server-info: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("", "GET", "/v1/auth/status", nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"pairing_enabled":true`) {
		t.Errorf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("", "GET", "/v1/shops", nil, ""); rec.Code != 401 || !strings.Contains(rec.Body.String(), "missing bearer token") {
		t.Errorf("no token: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("st_nope", "GET", "/v1/shops", nil, ""); rec.Code != 401 || !strings.Contains(rec.Body.String(), "invalid token") {
		t.Errorf("bad token: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("", "POST", "/v1/auth/pair", strings.NewReader(`{"password":"wrong","device_name":"x"}`), "application/json"); rec.Code != 401 {
		t.Errorf("wrong password: %d", rec.Code)
	}
	rec := f.do("", "POST", "/v1/auth/pair", strings.NewReader(`{"password":"correct horse battery","device_name":"phone"}`), "application/json")
	if rec.Code != 201 {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}
	var paired struct{ Token string }
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	paired.Token, _ = m["token"].(string)
	if rec := f.do(paired.Token, "POST", "/v1/shops", strings.NewReader(`{"name":"AH"}`), "application/json"); rec.Code != 201 {
		t.Errorf("create shop: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(paired.Token, "GET", "/v1/shops", nil, ""); !strings.Contains(rec.Body.String(), "AH") {
		t.Errorf("list: %s", rec.Body)
	}
}
