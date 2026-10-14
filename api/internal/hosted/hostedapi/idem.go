package hostedapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"shop/internal/hosted/control"
)

const maxIdemBody = 24 << 20

// idempotent implements the Idempotency-Key header (contract 8.2) for mutating requests: the first
// answer is kept for 24 hours and replayed with Idempotent-Replay: true.
func (a *API) idempotent(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			key = ""
		}
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 100 {
			a.fail(w, r, invalid("Idempotency-Key is at most 100 characters"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIdemBody))
		if err != nil {
			a.fail(w, r, newErr(http.StatusRequestEntityTooLarge, "file_too_large", "The request body is too large."))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(body)
		res, rec, err := a.Store.BeginIdempotent(r.Context(), scope, key, r.Method, r.URL.Path, hex.EncodeToString(sum[:]), a.Cfg.IdempotencyTTL)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		switch res {
		case control.IdemMismatch:
			a.fail(w, r, newErr(http.StatusConflict, "idempotency_key_reused", "This Idempotency-Key was used for a different request."))
		case control.IdemInProgress:
			one := 1
			a.fail(w, r, &httpErr{Status: http.StatusConflict, Code: "request_in_progress", Detail: "The first request with this key is still running.",
				Extra: func(p *problem) { p.RetryAfterSeconds = &one }, Header: map[string]string{"Retry-After": "1"}})
		case control.IdemReplay:
			if rec.ContentType != "" {
				w.Header().Set("Content-Type", rec.ContentType)
			}
			if rec.Location != "" {
				w.Header().Set("Location", rec.Location)
			}
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(rec.Status)
			_, _ = w.Write(rec.Response)
		default:
			cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
			func() {
				defer func() {
					if v := recover(); v != nil {
						_ = a.Store.ForgetIdempotent(r.Context(), scope, key)
						panic(v)
					}
				}()
				next.ServeHTTP(cw, r)
			}()
			ct := cw.Header().Get("Content-Type")
			jsonish := strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "application/problem+json") || cw.status == http.StatusNoContent
			if cw.overflow || !jsonish || !storable(cw.status) {
				_ = a.Store.ForgetIdempotent(r.Context(), scope, key)
				return
			}
			if err := a.Store.FinishIdempotent(r.Context(), scope, key, cw.status, ct, cw.Header().Get("Location"), cw.buf.Bytes()); err != nil {
				a.Log.Error("store idempotent response", "err", err)
			}
		}
	})
}

// storable: successes and deterministic client errors are kept; auth, quota and server errors are
// not (a retry after fixing the cause must run for real).
func storable(status int) bool {
	if status >= 200 && status < 300 {
		return true
	}
	switch status {
	case 400, 404, 409, 410, 413, 422:
		return true
	}
	return false
}

type captureWriter struct {
	http.ResponseWriter
	status   int
	buf      bytes.Buffer
	overflow bool
}

func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.buf.Len()+len(b) > 1<<20 {
		c.overflow = true
	} else {
		c.buf.Write(b)
	}
	return c.ResponseWriter.Write(b)
}

func (c *captureWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
