package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"shop/internal/apitypes"
	"shop/internal/autoprod"
	"shop/internal/categories"
	"shop/internal/english"
	"shop/internal/products"
	"shop/internal/receipts"
	"shop/internal/shopping"
	"shop/internal/shopsuggest"
)

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

// writeProblemExt adds a stable machine-readable code (and a count) to a problem.
func writeProblemExt(w http.ResponseWriter, status int, title, detail, code string, productCount *int64) {
	p := apitypes.Problem{Status: status, Title: title, Code: &code}
	if detail != "" {
		p.Detail = &detail
	}
	if productCount != nil {
		n := int(*productCount)
		p.ProductCount = &n
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// writeErr maps domain errors to HTTP problems.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var dup receipts.DuplicateError
	var bad badRequest
	var hasProducts categories.HasProductsError
	switch {
	case errors.As(err, &bad):
		writeProblem(w, http.StatusBadRequest, "bad request", bad.Error())
	case errors.As(err, &hasProducts):
		writeProblemExt(w, http.StatusConflict, "category has products", err.Error(), "category_has_products", &hasProducts.Count)
	case errors.Is(err, categories.ErrNameTaken):
		writeProblemExt(w, http.StatusConflict, "category name taken", err.Error(), "category_name_taken", nil)
	case errors.Is(err, categories.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not found", "")
	case errors.Is(err, categories.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid input", err.Error())
	case errors.Is(err, autoprod.ErrDifferentBarcodes):
		writeProblem(w, http.StatusConflict, "These two have different barcodes.", "")
	case errors.Is(err, autoprod.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not found", "")
	case errors.Is(err, autoprod.ErrInvalid):
		writeProblem(w, http.StatusUnprocessableEntity, "cannot do that", err.Error())
	case errors.Is(err, autoprod.ErrConflict), errors.Is(err, autoprod.ErrGone), errors.Is(err, autoprod.ErrDisabled):
		writeProblem(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, shopsuggest.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not found", "")
	case errors.Is(err, shopsuggest.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid input", err.Error())
	case errors.Is(err, shopsuggest.ErrConfirmed):
		writeProblem(w, http.StatusConflict, "receipt confirmed", err.Error())
	case errors.Is(err, receipts.ErrNotFound), errors.Is(err, products.ErrNotFound), errors.Is(err, shopping.ErrNotFound), errors.Is(err, english.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		writeProblem(w, http.StatusNotFound, "not found", "")
	case errors.Is(err, receipts.ErrInvalid), errors.Is(err, products.ErrInvalid), errors.Is(err, shopping.ErrInvalid), errors.Is(err, english.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid input", err.Error())
	case errors.Is(err, receipts.ErrUnsupported), errors.Is(err, products.ErrBadImage):
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported media type", err.Error())
	case errors.Is(err, shopping.ErrGone):
		writeProblem(w, http.StatusGone, "item was deleted", "this client_id's item has since been deleted")
	case errors.Is(err, products.ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, receipts.ErrEdited):
		writeProblem(w, http.StatusConflict, "receipt edited", err.Error())
	case errors.As(err, &dup):
		writeProblem(w, http.StatusConflict, "duplicate receipt", strconv.FormatInt(dup.ID, 10))
	default:
		s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "")
	}
}

type badRequest struct{ error }

func badf(format string, a ...any) error { return badRequest{fmt.Errorf(format, a...)} }

// decode reads a JSON body (max 1 MB), rejecting unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badf("invalid JSON: %v", err)
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badf("invalid id %q", r.PathValue("id"))
	}
	return id, nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
