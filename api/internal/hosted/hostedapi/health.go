package hostedapi

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// publicHealth answers GET /healthz. The gateway's answer names the OCR engine, shows OCR error
// text (which can include internal addresses) and counts the open households; a stranger on the
// internet gets only the status. The full answer is for a direct loopback caller (the container's
// own healthcheck, an operator on the host: no X-Forwarded-For, so not through the proxy) and for
// GET /admin/v1/health on the admin listener.
func (a *API) publicHealth(w http.ResponseWriter, r *http.Request) {
	if isLoopback(r.RemoteAddr) && r.Header.Get("X-Forwarded-For") == "" {
		a.gwH.ServeHTTP(w, r)
		return
	}
	rec := &bufWriter{h: http.Header{}, status: http.StatusOK}
	a.gwH.ServeHTTP(rec, r)
	var full struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.buf.Bytes(), &full); err != nil || full.Status == "" {
		full.Status = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": full.Status})
}

// adminHealth is the full health answer for the admin listener.
func (a *API) adminHealth(w http.ResponseWriter, r *http.Request) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/healthz"
	a.gwH.ServeHTTP(w, r2)
}

type bufWriter struct {
	h      http.Header
	buf    bytes.Buffer
	status int
}

func (b *bufWriter) Header() http.Header         { return b.h }
func (b *bufWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufWriter) WriteHeader(code int)        { b.status = code }
