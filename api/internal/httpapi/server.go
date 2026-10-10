// Package httpapi serves the REST API (and, later, mounts MCP).
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"pantry/internal/apitypes"
	"pantry/internal/ocr"
	"pantry/internal/store"
	"pantry/internal/version"
)

type Server struct {
	db          *sql.DB
	ocr         *ocr.Client
	ocrFallback *ocr.Client // nil when not configured
	log         *slog.Logger
}

func New(db *sql.DB, ocrClient, ocrFallback *ocr.Client, log *slog.Logger) *Server {
	return &Server{db: db, ocr: ocrClient, ocrFallback: ocrFallback, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /version", s.version)
	return s.recoverer(s.logRequests(mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]apitypes.Check{"db": check(s.db.PingContext(ctx))}
	checks["ocr"] = s.ocrCheck(ctx, s.ocr)
	if s.ocrFallback != nil {
		checks["ocr_fallback"] = s.ocrCheck(ctx, s.ocrFallback)
	}

	h := apitypes.Health{Status: apitypes.HealthStatusOk, Checks: checks}
	code := http.StatusOK
	for _, c := range checks {
		if !c.Ok {
			h.Status = apitypes.HealthStatusDegraded
			code = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, code, h)
}

func (s *Server) ocrCheck(ctx context.Context, c *ocr.Client) apitypes.Check {
	h, err := c.Health(ctx)
	if err != nil {
		return check(err)
	}
	detail := h.Engine
	if !h.Ready {
		detail += " (loading)"
	}
	return apitypes.Check{Ok: h.Ready, Detail: &detail}
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	v, err := store.MigrationVersion(r.Context(), s.db)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "schema version unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apitypes.Version{
		Version:       version.Version,
		Commit:        version.Commit,
		SchemaVersion: v,
	})
}

func check(err error) apitypes.Check {
	if err != nil {
		msg := err.Error()
		return apitypes.Check{Ok: false, Detail: &msg}
	}
	return apitypes.Check{Ok: true}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, code int, title, detail string) {
	p := apitypes.Problem{Status: code, Title: title}
	if detail != "" {
		p.Detail = &detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(p)
}
