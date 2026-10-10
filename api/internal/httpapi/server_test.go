package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"pantry/internal/apitypes"
	"pantry/internal/httpapi"
	"pantry/internal/ocr"
	"pantry/internal/store"
)

func newServer(t *testing.T, ocrURL string) http.Handler {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "pantry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := store.Migrate(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.New(conn, ocr.New(ocrURL), nil, log).Handler()
}

func fakeOCR(ready bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ocr.Health{Engine: "fake", Ready: ready})
	}))
}

func getHealth(t *testing.T, h http.Handler) (int, apitypes.Health) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body apitypes.Health
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	return rec.Code, body
}

func TestHealthOK(t *testing.T) {
	o := fakeOCR(true)
	defer o.Close()

	code, body := getHealth(t, newServer(t, o.URL))
	if code != http.StatusOK || body.Status != apitypes.HealthStatusOk {
		t.Fatalf("got %d %+v", code, body)
	}
	if !body.Checks["db"].Ok || !body.Checks["ocr"].Ok {
		t.Fatalf("checks = %+v", body.Checks)
	}
}

func TestHealthDegradedWhenOCRLoading(t *testing.T) {
	o := fakeOCR(false)
	defer o.Close()

	code, body := getHealth(t, newServer(t, o.URL))
	if code != http.StatusServiceUnavailable || body.Status != apitypes.HealthStatusDegraded {
		t.Fatalf("got %d %+v", code, body)
	}
}

func TestHealthDegradedWhenOCRDown(t *testing.T) {
	code, body := getHealth(t, newServer(t, "http://127.0.0.1:1"))
	if code != http.StatusServiceUnavailable || body.Checks["ocr"].Ok {
		t.Fatalf("got %d %+v", code, body)
	}
}

func TestVersion(t *testing.T) {
	rec := httptest.NewRecorder()
	newServer(t, "http://127.0.0.1:1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	var v apitypes.Version
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 || v.SchemaVersion < 2 {
		t.Fatalf("got %d %+v %v", rec.Code, v, err)
	}
}
