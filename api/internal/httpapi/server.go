// Package httpapi serves the REST API (and mounts MCP).
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"shop/internal/apitypes"
	"shop/internal/autoprod"
	"shop/internal/backup"
	"shop/internal/categories"
	"shop/internal/english"
	"shop/internal/listplan"
	"shop/internal/logos"
	"shop/internal/ocr"
	"shop/internal/products"
	"shop/internal/receipts"
	"shop/internal/search"
	"shop/internal/shopping"
	"shop/internal/shopsuggest"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/version"
)

type Deps struct {
	DB          *sql.DB
	OCR         *ocr.Client
	OCRFallback *ocr.Client // nil when not configured
	Receipts    *receipts.Service
	Products    *products.Service
	Stats       *stats.Service
	List        *shopping.Service
	Plan        *listplan.Service // product groups and the shop plan; created from DB when nil
	English     *english.Service
	Search      *search.Service
	Auto        *autoprod.Service // automatic products; nil disables those routes' work
	Logos       *logos.Cache      // nil or disabled: no logos
	Location    *time.Location
	Log         *slog.Logger
	DataDir     string // for backup status and storage size; optional
	// Extra routes mounted behind auth (e.g. MCP), keyed by pattern.
	Authed map[string]http.Handler
}

type Server struct {
	Deps
	q          *db.Queries
	pairing    *pairLimiter
	suggest    *shopsuggest.Service
	categories *categories.Service
}

func New(d Deps) *Server {
	if d.Location == nil {
		d.Location = time.UTC
	}
	if d.Plan == nil {
		d.Plan = listplan.New(d.DB)
	}
	return &Server{Deps: d, q: db.New(d.DB), pairing: newPairLimiter(), suggest: shopsuggest.NewService(d.DB), categories: categories.New(d.DB)}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()

	api.HandleFunc("GET /v1/auth/me", s.me)
	api.HandleFunc("GET /v1/auth/devices", s.listDevices)
	api.HandleFunc("DELETE /v1/auth/devices/{id}", s.revokeDevice)

	api.HandleFunc("GET /v1/shops", s.listShops)
	api.HandleFunc("POST /v1/shops", s.createShop)
	api.HandleFunc("GET /v1/shops/{id}", s.getShop)
	api.HandleFunc("PATCH /v1/shops/{id}", s.updateShop)
	api.HandleFunc("DELETE /v1/shops/{id}", s.deleteShop)
	api.HandleFunc("GET /v1/shops/{id}/logo", s.shopLogo)

	api.HandleFunc("GET /v1/categories", s.listCategories)
	api.HandleFunc("POST /v1/categories", s.createCategory)
	api.HandleFunc("PATCH /v1/categories/{id}", s.updateCategory)
	api.HandleFunc("DELETE /v1/categories/{id}", s.deleteCategory)

	api.HandleFunc("GET /v1/receipts", s.listReceipts)
	api.HandleFunc("POST /v1/receipts/{id}/list-checks/undo", s.undoListChecks)
	api.HandleFunc("POST /v1/receipts", s.uploadReceipt)
	api.HandleFunc("GET /v1/receipts/counts", s.receiptCounts)
	api.HandleFunc("POST /v1/receipts/{id}/duplicate/keep", s.keepDuplicate)
	api.HandleFunc("DELETE /v1/receipts/{id}/duplicate/keep", s.undoKeepDuplicate)
	api.HandleFunc("GET /v1/receipts/{id}", s.getReceipt)
	api.HandleFunc("PATCH /v1/receipts/{id}", s.updateReceipt)
	api.HandleFunc("DELETE /v1/receipts/{id}", s.deleteReceipt)
	api.HandleFunc("GET /v1/receipts/{id}/shop-suggestion", s.getShopSuggestion)
	api.HandleFunc("POST /v1/receipts/{id}/shop-suggestion/accept", s.acceptShopSuggestion)
	api.HandleFunc("POST /v1/receipts/{id}/shop-suggestion/reject", s.rejectShopSuggestion)
	api.HandleFunc("GET /v1/receipts/{id}/image", s.receiptImage)
	api.HandleFunc("POST /v1/receipts/{id}/confirm", s.confirmReceipt)
	api.HandleFunc("POST /v1/receipts/{id}/reparse", s.requeue(false))
	api.HandleFunc("POST /v1/receipts/{id}/reocr", s.requeue(true))
	api.HandleFunc("POST /v1/receipts/{id}/lines", s.createLine)
	api.HandleFunc("PATCH /v1/lines/{id}", s.updateLine)
	api.HandleFunc("DELETE /v1/lines/{id}", s.deleteLine)

	api.HandleFunc("GET /v1/products/review-queue", s.getReviewQueue)
	api.HandleFunc("GET /v1/products/autopopulate/backfill", s.getBackfill)
	api.HandleFunc("POST /v1/products/autopopulate/backfill", s.startBackfill)
	api.HandleFunc("POST /v1/products/{id}/restore", s.restoreProduct)
	api.HandleFunc("POST /v1/products/{id}/review", s.reviewProduct)
	api.HandleFunc("POST /v1/products/{id}/merge", s.mergeProduct)
	api.HandleFunc("POST /v1/products/{id}/refresh", s.refreshProduct)
	api.HandleFunc("POST /v1/product-merges/{id}/undo", s.undoProductMerge)
	api.HandleFunc("POST /v1/lines/{id}/split-product", s.splitLineProduct)
	api.HandleFunc("POST /v1/barcodes/{code}/product", s.createBarcodeProduct)
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

	api.HandleFunc("GET /v1/stats/spend", s.getSpend)
	api.HandleFunc("GET /v1/stats/products/{id}/prices", s.getPriceHistory)
	api.HandleFunc("GET /v1/stats/products/{id}/compare", s.comparePrices)
	api.HandleFunc("GET /v1/stats/price-changes", s.getPriceChanges)
	api.HandleFunc("GET /v1/stats/frequent-items", s.getFrequentItems)

	api.HandleFunc("GET /v1/list", s.getShoppingList)
	api.HandleFunc("POST /v1/list", s.addListItem)
	api.HandleFunc("DELETE /v1/list/checked", s.clearCheckedItems)
	api.HandleFunc("GET /v1/list/changes", s.listChanges)
	api.HandleFunc("GET /v1/list/groups", s.listGroups)
	api.HandleFunc("POST /v1/list/groups", s.createListGroup)
	api.HandleFunc("GET /v1/list/groups/{id}", s.getListGroup)
	api.HandleFunc("PATCH /v1/list/groups/{id}", s.updateListGroup)
	api.HandleFunc("DELETE /v1/list/groups/{id}", s.deleteListGroup)
	api.HandleFunc("POST /v1/list/groups/{id}/items", s.addListGroupItems)
	api.HandleFunc("POST /v1/list/groups/{id}/restore", s.restoreListGroup)
	api.HandleFunc("POST /v1/list/groups/{id}/instantiate", s.copyListGroup(false))
	api.HandleFunc("POST /v1/list/groups/{id}/save-as-template", s.copyListGroup(true))
	api.HandleFunc("POST /v1/list/{id}/move", s.moveListItem)
	api.HandleFunc("GET /v1/list/suggestions", s.listSuggestions)
	api.HandleFunc("POST /v1/list/suggestions/{id}/accept", s.acceptSuggestion)
	api.HandleFunc("POST /v1/list/suggestions/{id}/dismiss", s.dismissSuggestion)
	api.HandleFunc("PATCH /v1/list/{id}", s.updateListItem)
	api.HandleFunc("DELETE /v1/list/{id}", s.deleteListItem)
	api.HandleFunc("POST /v1/list/{id}/check", s.setListItemChecked(true))
	api.HandleFunc("POST /v1/list/{id}/uncheck", s.setListItemChecked(false))

	api.HandleFunc("GET /v1/list/plan", s.getListPlan)
	api.HandleFunc("GET /v1/list/plan/settings", s.getPlanSettings)
	api.HandleFunc("PATCH /v1/list/plan/settings", s.updatePlanSettings)
	api.HandleFunc("GET /v1/product-groups", s.listProductGroups)
	api.HandleFunc("POST /v1/product-groups", s.confirmProductGroup)
	api.HandleFunc("POST /v1/product-groups/confirm", s.confirmProductGroup)
	api.HandleFunc("POST /v1/product-groups/deny", s.denyProductGroupPair)
	api.HandleFunc("GET /v1/product-groups/suggestions", s.listProductGroupSuggestions)
	api.HandleFunc("GET /v1/product-groups/{id}", s.getProductGroup)
	api.HandleFunc("PATCH /v1/product-groups/{id}", s.renameProductGroup)
	api.HandleFunc("DELETE /v1/product-groups/{id}", s.deleteProductGroup)
	api.HandleFunc("POST /v1/product-groups/{id}/merge", s.mergeProductGroup)
	api.HandleFunc("POST /v1/product-groups/{id}/split", s.splitProductGroup)
	api.HandleFunc("POST /v1/product-group-ops/{id}/undo", s.undoProductGroupOp)

	api.HandleFunc("GET /v1/search", s.search)
	api.HandleFunc("GET /v1/glossary", s.listGlossary)
	api.HandleFunc("POST /v1/glossary/preview", s.previewGlossary)
	api.HandleFunc("PUT /v1/glossary/{dutch}", s.teachWord)
	api.HandleFunc("DELETE /v1/glossary/{dutch}", s.forgetWord)
	api.HandleFunc("GET /v1/translations/todo", s.translationTodo)
	api.HandleFunc("GET /v1/translate", s.previewTranslation)

	for pattern, h := range s.Authed {
		api.Handle(pattern, h)
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", s.health)
	root.HandleFunc("GET /version", s.version)
	root.HandleFunc("GET /v1/auth/status", s.authStatus)
	root.HandleFunc("POST /v1/auth/pair", s.pair)
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

	if s.DataDir != "" {
		checks["storage"] = s.storageCheck()
		if c, ok := s.backupCheck(); ok {
			checks["backup"] = c
		}
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

// backupStaleAfter: daily backups, plus slack for a missed night.
const backupStaleAfter = 36 * time.Hour

// backupCheck reports the last backup run. It is absent until backups
// have run at least once, so a dev setup without a NAS stays healthy.
func (s *Server) backupCheck() (apitypes.Check, bool) {
	st, err := backup.ReadStatus(filepath.Join(s.DataDir, "backup_status.json"))
	if errors.Is(err, os.ErrNotExist) {
		return apitypes.Check{}, false
	}
	if err != nil {
		return check(err), true
	}
	age := time.Since(st.At).Round(time.Minute)
	switch {
	case !st.OK:
		d := fmt.Sprintf("last backup failed %s ago: %s", age, st.Error)
		return apitypes.Check{Ok: false, Detail: &d}, true
	case age > backupStaleAfter:
		d := fmt.Sprintf("stale: last backup %s ago", age)
		return apitypes.Check{Ok: false, Detail: &d}, true
	}
	d := fmt.Sprintf("%s, %s ago", st.Snapshot, age)
	return apitypes.Check{Ok: true, Detail: &d}, true
}

func (s *Server) storageCheck() apitypes.Check {
	var size int64
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(filepath.Join(s.DataDir, "shop.db"+suffix)); err == nil {
			size += fi.Size()
		}
	}
	d := fmt.Sprintf("db %.1f MB", float64(size)/(1<<20))
	return apitypes.Check{Ok: true, Detail: &d}
}
