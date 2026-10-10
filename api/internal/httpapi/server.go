// Package httpapi serves the REST API (and mounts MCP).
package httpapi

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"pantry/internal/apitypes"
	"pantry/internal/ocr"
	"pantry/internal/products"
	"pantry/internal/receipts"
	"pantry/internal/store"
	"pantry/internal/store/db"
	"pantry/internal/version"
)

type Deps struct {
	DB          *sql.DB
	OCR         *ocr.Client
	OCRFallback *ocr.Client // nil when not configured
	Receipts    *receipts.Service
	Products    *products.Service
	Location    *time.Location
	Log         *slog.Logger
	// Extra routes mounted behind auth (e.g. MCP), keyed by pattern.
	Authed map[string]http.Handler
}

type Server struct {
	Deps
	q *db.Queries
}

func New(d Deps) *Server {
	if d.Location == nil {
		d.Location = time.UTC
	}
	return &Server{Deps: d, q: db.New(d.DB)}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()

	api.HandleFunc("GET /v1/shops", s.listShops)
	api.HandleFunc("POST /v1/shops", s.createShop)
	api.HandleFunc("GET /v1/shops/{id}", s.getShop)
	api.HandleFunc("PATCH /v1/shops/{id}", s.updateShop)
	api.HandleFunc("DELETE /v1/shops/{id}", s.deleteShop)

	api.HandleFunc("GET /v1/categories", s.listCategories)
	api.HandleFunc("POST /v1/categories", s.createCategory)
	api.HandleFunc("PATCH /v1/categories/{id}", s.updateCategory)
	api.HandleFunc("DELETE /v1/categories/{id}", s.deleteCategory)

	api.HandleFunc("GET /v1/receipts", s.listReceipts)
	api.HandleFunc("POST /v1/receipts", s.uploadReceipt)
	api.HandleFunc("GET /v1/receipts/{id}", s.getReceipt)
	api.HandleFunc("PATCH /v1/receipts/{id}", s.updateReceipt)
	api.HandleFunc("DELETE /v1/receipts/{id}", s.deleteReceipt)
	api.HandleFunc("GET /v1/receipts/{id}/image", s.receiptImage)
	api.HandleFunc("POST /v1/receipts/{id}/confirm", s.confirmReceipt)
	api.HandleFunc("POST /v1/receipts/{id}/reparse", s.requeue(false))
	api.HandleFunc("POST /v1/receipts/{id}/reocr", s.requeue(true))
	api.HandleFunc("POST /v1/receipts/{id}/lines", s.createLine)
	api.HandleFunc("PATCH /v1/lines/{id}", s.updateLine)
	api.HandleFunc("DELETE /v1/lines/{id}", s.deleteLine)

	api.HandleFunc("GET /v1/products", s.listProducts)
	api.HandleFunc("POST /v1/products", s.createProduct)
	api.HandleFunc("GET /v1/barcodes/{code}", s.lookupBarcode)
	api.HandleFunc("GET /v1/products/{id}", s.getProduct)
	api.HandleFunc("PATCH /v1/products/{id}", s.updateProduct)
	api.HandleFunc("DELETE /v1/products/{id}", s.deleteProduct)
	api.HandleFunc("GET /v1/products/{id}/image", s.productImage)
	api.HandleFunc("PUT /v1/products/{id}/image", s.putProductImage)
	api.HandleFunc("PUT /v1/products/{id}/nutrition", s.putNutrition)
	api.HandleFunc("DELETE /v1/products/{id}/nutrition", s.deleteNutrition)
	api.HandleFunc("GET /v1/products/{id}/aliases", s.listAliases)
	api.HandleFunc("GET /v1/products/{id}/unmatched-lines", s.unmatchedLines)
	api.HandleFunc("DELETE /v1/aliases/{id}", s.deleteAlias)
	api.HandleFunc("PUT /v1/lines/{id}/product", s.linkLine)

	for pattern, h := range s.Authed {
		api.Handle(pattern, h)
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", s.health)
	root.HandleFunc("GET /version", s.version)
	root.Handle("/", s.requireToken(api))
	return s.recoverer(s.logRequests(root))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]apitypes.Check{"db": check(s.DB.PingContext(ctx))}
	checks["ocr"] = s.ocrCheck(ctx, s.OCR)
	if s.OCRFallback != nil {
		checks["ocr_fallback"] = s.ocrCheck(ctx, s.OCRFallback)
	}
	if counts, err := s.q.CountJobsByStatus(ctx); err == nil {
		var queued, failed int64
		for _, c := range counts {
			switch c.Status {
			case "queued", "running":
				queued += c.N
			case "failed":
				failed += c.N
			}
		}
		detail := itoa(queued) + " pending, " + itoa(failed) + " failed"
		checks["jobs"] = apitypes.Check{Ok: true, Detail: &detail}
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
	v, err := store.MigrationVersion(r.Context(), s.DB)
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
