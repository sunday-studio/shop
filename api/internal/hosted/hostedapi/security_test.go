package hostedapi_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shop/internal/hosted/hostedapi"
	w "shop/internal/hosted/hostedworld"
)

// A receipt upload is read into memory (up to 20 MB). Only UploadsInFlight of them may run at once
// in the whole process, whoever sends them, so a crowd of uploads cannot exhaust the container.
func TestUploadsInFlightAreCappedProcessWide(t *testing.T) {
	world := w.New(t, w.NoRates, func(c *hostedapi.Config) { c.Rates.UploadsInFlight = 1 })
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	bob := world.SignIn("sub-bob", "Bob")
	world.Subscribe(bob, "otx-bob")

	// Ama's upload stalls while its body is being read.
	pr, pw := io.Pipe()
	req := httptest.NewRequest("POST", "/v1/receipts", pr)
	req.Header.Set("Authorization", "Bearer "+ama.Token)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		world.Handler.ServeHTTP(rec, req)
		done <- rec
	}()
	time.Sleep(200 * time.Millisecond) // the handler is now waiting for the body, holding the slot

	r := upload(world, bob.Token, png1(9))
	want(t, r, 429, "rate_limited")
	if r.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}

	// finish Ama's request (an empty form is a 400, and frees the slot)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.Close()
	go func() { _, _ = pw.Write([]byte("--x--\r\n")); _ = pw.Close() }()
	select {
	case rec := <-done:
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("the first upload must not be refused: %s", rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first upload never finished")
	}
	want(t, upload(world, bob.Token, png1(9)), 202, "")
}

// The same cap covers product pictures (multipart PUT), which are decoded in memory too.
func TestProductImageUploadsShareTheCap(t *testing.T) {
	world := w.New(t, w.NoRates, func(c *hostedapi.Config) { c.Rates.UploadsInFlight = 1 })
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	bob := world.SignIn("sub-bob", "Bob")
	world.Subscribe(bob, "otx-bob")

	pr, pw := io.Pipe()
	req := httptest.NewRequest("PUT", "/v1/products/1/image", pr)
	req.Header.Set("Authorization", "Bearer "+ama.Token)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	done := make(chan struct{})
	go func() {
		world.Handler.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	want(t, upload(world, bob.Token, png1(5)), 429, "rate_limited")
	_ = pw.CloseWithError(io.ErrClosedPipe)
	<-done
	want(t, upload(world, bob.Token, png1(5)), 202, "")
}

// The address behind the per-address limits is the last X-Forwarded-For entry (set by our proxy),
// an IPv6 address counts as its /64, and a garbage entry falls back to the socket address.
func TestRateLimitKeyIP(t *testing.T) {
	world := w.New(t, func(c *hostedapi.Config) { c.TrustProxy = true; c.Rates.NoncePerMin = 2 })
	post := func(remote, xff string) w.Resp {
		var h []string
		if xff != "" {
			h = []string{"X-Forwarded-For", xff}
		}
		return world.DoFrom(remote, "POST", "/v1/auth/apple/nonce", "", nil, h...)
	}
	// a client cannot pick its own bucket by sending its own X-Forwarded-For first
	post("10.0.0.2:1", "1.1.1.1, 203.0.113.9")
	post("10.0.0.2:1", "2.2.2.2, 203.0.113.9")
	want(t, post("10.0.0.2:1", "3.3.3.3, 203.0.113.9"), 429, "rate_limited")
	// every address of one IPv6 /64 shares a bucket
	post("10.0.0.2:1", "2001:db8:1:2::1")
	post("10.0.0.2:1", "2001:db8:1:2:ffff::7")
	want(t, post("10.0.0.2:1", "2001:db8:1:2:abcd::9"), 429, "rate_limited")
	want(t, post("10.0.0.2:1", "2001:db8:1:3::1"), 201, "")
	// garbage in the header: the socket address is used, never the garbage as a key
	post("10.0.0.3:1", "not-an-ip")
	post("10.0.0.3:1", "also bad")
	want(t, post("10.0.0.3:1", "x"), 429, "rate_limited")
}
