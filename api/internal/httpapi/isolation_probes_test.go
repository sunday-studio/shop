//go:build isolation

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// M is a JSON object literal.
type M = map[string]any

// probe says how one HTTP operation is called by the attacker (household B) against the victim
// (household A), and what a safe answer looks like.
type probe struct {
	// Body builds the request body. v is the victim, x the attacker (the caller). nil: a default body.
	Body func(v, x *household) any
	// Query is appended to the path ("?" included).
	Query func(v, x *household) string
	// Scoped marks operations that are not addressed by a victim id (lists, creates, lookups by
	// key). They may answer anything except the victim's data; id-addressed operations must answer 404.
	Scoped bool
	// Own marks list operations whose answer must contain the caller's own data (the test would
	// otherwise pass on an empty answer).
	Own bool
	// Ctrl lists the status codes the owner gets when calling the same thing on its own data
	// (default: any 2xx). It proves the probe is meaningful.
	Ctrl []int
	// Exempt: not probed, with the reason. Only public operations without tenant data.
	Exempt string
	// Validates marks operations that check the body before looking up the id, so the 404 can be a 400.
	Validates string
	// IDs names the id set (of the caller) a list answer must stay inside.
	IDs func(h *household) map[int64]bool
	// Echo marks probes that send the victim's key (a word) and get it echoed back; for these only
	// the victim's taught meaning counts as a leak.
	Echo bool
}

func bodyOf(f func(v, x *household) any) func(v, x *household) any { return f }

var emptyObj = func(v, x *household) any { return M{} }

// probes is keyed "METHOD /v1/path/{template}" exactly as in openapi.yaml.
var probes = map[string]probe{
	"GET /healthz":        {Exempt: "public: process health only (db ping, queue counts of the process), no household data"},
	"GET /version":        {Exempt: "public: build version and schema version, no household data"},
	"GET /v1/auth/status": {Exempt: "public: server name, version, whether pairing is set up; no household data"},
	"GET /v1/server-info": {Exempt: "public: mode, api level, login methods, features; no household data"},
	"POST /v1/auth/pair":  {Exempt: "public pairing endpoint of the single-household product; a multi-household server does not offer it (404)"},

	"GET /v1/auth/me":              {Scoped: true},
	"GET /v1/auth/devices":         {Scoped: true, Own: true},
	"DELETE /v1/auth/devices/{id}": {Ctrl: []int{204}},

	"GET /v1/shops":           {Scoped: true, Own: true},
	"POST /v1/shops":          {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"name": "New shop " + x.marker} })},
	"GET /v1/shops/{id}":      {},
	"PATCH /v1/shops/{id}":    {Body: bodyOf(func(v, x *household) any { return M{"name": "hacked by " + x.marker} })},
	"DELETE /v1/shops/{id}":   {Ctrl: []int{204}},
	"GET /v1/shops/{id}/logo": {Ctrl: []int{200}},

	"GET /v1/categories":         {Scoped: true, Own: true},
	"POST /v1/categories":        {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"name": "New category " + x.marker} })},
	"PATCH /v1/categories/{id}":  {Body: bodyOf(func(v, x *household) any { return M{"name": "hacked " + x.marker} })},
	"DELETE /v1/categories/{id}": {Ctrl: []int{204}},

	"GET /v1/receipts":                              {Scoped: true, IDs: func(h *household) map[int64]bool { return h.ids.ReceiptIDs }, Query: ids2("?limit=50")},
	"POST /v1/receipts":                             {Scoped: true, Body: bodyOf(func(v, x *household) any { return multipartImage("image", pngBytes(200), nil) })},
	"GET /v1/receipts/counts":                       {Scoped: true},
	"GET /v1/receipts/{id}":                         {},
	"PATCH /v1/receipts/{id}":                       {Body: bodyOf(func(v, x *household) any { return M{"total_cents": 1} })},
	"DELETE /v1/receipts/{id}":                      {Ctrl: []int{204}},
	"POST /v1/receipts/{id}/duplicate/keep":         {Ctrl: []int{200, 204}},
	"DELETE /v1/receipts/{id}/duplicate/keep":       {Ctrl: []int{200, 204}},
	"GET /v1/receipts/{id}/shop-suggestion":         {},
	"POST /v1/receipts/{id}/shop-suggestion/accept": {Body: bodyOf(func(v, x *household) any { return M{"name": "Shop hacked " + x.marker} }), Ctrl: []int{200, 201, 404, 409}},
	"POST /v1/receipts/{id}/shop-suggestion/reject": {Ctrl: []int{200, 204, 404, 409}},
	"GET /v1/receipts/{id}/image":                   {},
	"POST /v1/receipts/{id}/confirm":                {Ctrl: []int{200, 409}},
	"POST /v1/receipts/{id}/reparse":                {Ctrl: []int{200, 202}},
	"POST /v1/receipts/{id}/reocr":                  {Ctrl: []int{200, 202}},
	"POST /v1/receipts/{id}/lines": {Body: bodyOf(func(v, x *household) any {
		return M{"raw_text": "extra " + x.marker, "kind": "item", "line_total_cents": 1}
	}), Ctrl: []int{201}},
	"POST /v1/receipts/{id}/list-checks/undo": {Ctrl: []int{200}},
	"PATCH /v1/lines/{id}": {Body: bodyOf(func(v, x *household) any {
		return M{"raw_text": "hacked " + x.marker, "kind": "item", "line_total_cents": 1}
	})},
	"DELETE /v1/lines/{id}":             {Ctrl: []int{200, 204}},
	"PUT /v1/lines/{id}/product":        {Body: bodyOf(func(v, x *household) any { return M{"product_id": v.ids.Product} })},
	"POST /v1/lines/{id}/split-product": {Ctrl: []int{200, 201, 400, 409}},

	"GET /v1/products":                        {Scoped: true, Own: true, Query: ids2("?limit=50")},
	"POST /v1/products":                       {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"name": "New product " + x.marker} })},
	"GET /v1/products/review-queue":           {Scoped: true},
	"GET /v1/products/autopopulate/backfill":  {Scoped: true},
	"POST /v1/products/autopopulate/backfill": {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"dry_run": true} }), Ctrl: []int{200, 202}},
	"POST /v1/products/{id}/review":           {Body: bodyOf(func(v, x *household) any { return M{"action": "accept"} }), Ctrl: []int{200, 409}},
	"POST /v1/products/{id}/merge":            {Body: bodyOf(func(v, x *household) any { return M{"into_product_id": v.ids.Product} })},
	"POST /v1/products/{id}/restore":          {Ctrl: []int{200, 409}},
	"POST /v1/products/{id}/refresh":          {Ctrl: []int{200, 409, 422}},
	"POST /v1/product-merges/{id}/undo":       {Ctrl: []int{200}},
	"GET /v1/barcodes/{code}":                 {Scoped: true, Ctrl: []int{200, 404}},
	"POST /v1/barcodes/{code}/product":        {Scoped: true, Ctrl: []int{200, 201, 404, 409, 422}},
	"GET /v1/products/{id}":                   {},
	"PATCH /v1/products/{id}":                 {Body: bodyOf(func(v, x *household) any { return M{"name": "hacked " + x.marker} })},
	"DELETE /v1/products/{id}":                {Ctrl: []int{200, 204}},
	"GET /v1/products/{id}/image":             {Ctrl: []int{200, 404}},
	"PUT /v1/products/{id}/image":             {Body: bodyOf(func(v, x *household) any { return multipartImage("image", pngBytes(201), nil) })},
	"PUT /v1/products/{id}/nutrition":         {Body: bodyOf(func(v, x *household) any { return M{"per": "100g", "energy_kcal": 1} })},
	"DELETE /v1/products/{id}/nutrition":      {Ctrl: []int{200, 204}},
	"GET /v1/products/{id}/aliases":           {},
	"GET /v1/products/{id}/unmatched-lines":   {},
	"DELETE /v1/aliases/{id}":                 {Ctrl: []int{200, 204}},

	"GET /v1/stats/spend":                 {Scoped: true, Query: ids2("?from=2026-01-01&to=2026-12-31&group_by=month")},
	"GET /v1/stats/products/{id}/prices":  {},
	"GET /v1/stats/products/{id}/compare": {},
	"GET /v1/stats/price-changes":         {Scoped: true},
	"GET /v1/stats/frequent-items":        {Scoped: true},

	"GET /v1/list":                               {Scoped: true, Own: true},
	"POST /v1/list":                              {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"text": "Bananen " + x.marker} })},
	"GET /v1/list/changes":                       {Scoped: true, Query: ids2("?since_rev=0")},
	"DELETE /v1/list/checked":                    {Scoped: true},
	"GET /v1/list/groups":                        {Scoped: true, Own: true},
	"POST /v1/list/groups":                       {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"name": "New group " + x.marker} })},
	"GET /v1/list/groups/{id}":                   {},
	"PATCH /v1/list/groups/{id}":                 {Body: bodyOf(func(v, x *household) any { return M{"name": "hacked " + x.marker} })},
	"DELETE /v1/list/groups/{id}":                {Ctrl: []int{200, 204}},
	"POST /v1/list/groups/{id}/items":            {Body: bodyOf(func(v, x *household) any { return M{"items": []any{M{"text": "extra " + x.marker}}} }), Ctrl: []int{200, 201}},
	"POST /v1/list/groups/{id}/restore":          {Ctrl: []int{200, 404, 409}}, // nothing was deleted in the owner's setup
	"POST /v1/list/groups/{id}/instantiate":      {Ctrl: []int{200, 201, 409}},
	"POST /v1/list/groups/{id}/save-as-template": {Ctrl: []int{200, 201, 409}},
	"GET /v1/list/suggestions":                   {Scoped: true},
	"POST /v1/list/suggestions/{id}/accept":      {Ctrl: []int{200, 201, 404, 409}},
	"POST /v1/list/suggestions/{id}/dismiss":     {Ctrl: []int{200, 204, 404, 409}},
	"PATCH /v1/list/{id}":                        {Body: bodyOf(func(v, x *household) any { return M{"text": "hacked " + x.marker} })},
	"DELETE /v1/list/{id}":                       {Ctrl: []int{200, 204}},
	"POST /v1/list/{id}/move":                    {Body: bodyOf(func(v, x *household) any { return M{"group_id": nil} })},
	"POST /v1/list/{id}/check":                   {},
	"POST /v1/list/{id}/uncheck":                 {},

	"GET /v1/list/plan":            {Scoped: true},
	"GET /v1/list/plan/settings":   {Scoped: true},
	"PATCH /v1/list/plan/settings": {Scoped: true, Body: bodyOf(func(v, x *household) any { return M{"max_stops": 1} })},

	"GET /v1/product-groups": {Scoped: true, Own: true},
	"POST /v1/product-groups": {Scoped: true, Body: bodyOf(func(v, x *household) any {
		return M{"product_ids": []int64{x.ids.Product, x.ids.Product2}, "name": "New pg " + x.marker}
	})},
	"POST /v1/product-groups/confirm": {Scoped: true, Body: bodyOf(func(v, x *household) any {
		return M{"product_ids": []int64{x.ids.Product, x.ids.Product2}, "name": "Confirmed pg " + x.marker}
	})},
	"POST /v1/product-groups/deny": {Scoped: true, Body: bodyOf(func(v, x *household) any {
		return M{"product_a_id": x.ids.Product, "product_b_id": x.ids.Product2}
	})},
	"GET /v1/product-groups/suggestions":   {Scoped: true},
	"GET /v1/product-groups/{id}":          {},
	"PATCH /v1/product-groups/{id}":        {Body: bodyOf(func(v, x *household) any { return M{"name": "hacked " + x.marker} })},
	"DELETE /v1/product-groups/{id}":       {Ctrl: []int{200, 204}},
	"POST /v1/product-groups/{id}/merge":   {Body: bodyOf(func(v, x *household) any { return M{"into_group_id": v.ids.ProductGroup2} })},
	"POST /v1/product-groups/{id}/split":   {Body: bodyOf(func(v, x *household) any { return M{"product_ids": []int64{v.ids.GroupMember}, "keep_together": false} })},
	"POST /v1/product-group-ops/{id}/undo": {Ctrl: []int{200, 409}},

	"GET /v1/search":              {Scoped: true, Own: true, Query: func(v, x *household) string { return "?q=" + x.marker }},
	"GET /v1/glossary":            {Scoped: true, Own: true},
	"POST /v1/glossary/preview":   {Scoped: true, Echo: true, Body: bodyOf(func(v, x *household) any { return M{"dutch": v.ids.Word, "english": "x"} })},
	"PUT /v1/glossary/{dutch}":    {Scoped: true, Echo: true, Body: bodyOf(func(v, x *household) any { return M{"english": "taught by " + x.marker} })},
	"DELETE /v1/glossary/{dutch}": {Ctrl: []int{200, 204}},
	"GET /v1/translations/todo":   {Scoped: true},
	"GET /v1/translate":           {Scoped: true, Echo: true, Query: func(v, x *household) string { return "?text=" + v.ids.Word }},
}

func ids2(q string) func(v, x *household) string { return func(v, x *household) string { return q } }

type specOp struct{ Method, Tmpl string }

func (o specOp) key() string { return o.Method + " " + o.Tmpl }

// specOps reads every operation of openapi.yaml (the same way TestEveryRouteRequiresAToken does).
func specOps(t *testing.T) []specOp {
	t.Helper()
	b, err := os.ReadFile("../../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pathRe := regexp.MustCompile(`^  (/[^:\s]+):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):`)
	var ops []specOp
	cur := ""
	for _, line := range strings.Split(string(b), "\n") {
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
		} else if m := methodRe.FindStringSubmatch(line); m != nil && cur != "" {
			if isHostedPath(cur) {
				continue // hosted-only routes are covered by internal/hosted/hostedapi tests and the hosted isolation world
			}
			ops = append(ops, specOp{strings.ToUpper(m[1]), cur})
		}
	}
	if len(ops) < 100 {
		t.Fatalf("only %d operations parsed from openapi.yaml", len(ops))
	}
	return ops
}

var pathVarRe = regexp.MustCompile(`\{[^}]+\}`)

// pathFor fills the template with the ids of the household that owns the resource.
func pathFor(method, tmpl string, ids seedIDs) string {
	if !pathVarRe.MatchString(tmpl) {
		return tmpl
	}
	if strings.HasSuffix(tmpl, "/instantiate") {
		return pathVarRe.ReplaceAllString(tmpl, fmt.Sprint(ids.Template))
	}
	if strings.HasPrefix(tmpl, "/v1/list/groups/") && strings.HasSuffix(tmpl, "/restore") {
		return pathVarRe.ReplaceAllString(tmpl, fmt.Sprint(ids.TrashGroup))
	}
	if strings.HasPrefix(tmpl, "/v1/products/") && (strings.HasSuffix(tmpl, "/review") || strings.HasSuffix(tmpl, "/merge")) {
		return pathVarRe.ReplaceAllString(tmpl, fmt.Sprint(ids.Review))
	}
	if method == "DELETE" && strings.HasPrefix(tmpl, "/v1/glossary/") {
		return pathVarRe.ReplaceAllString(tmpl, ids.Word2) // PUT teaches Word in the caller's own glossary, so DELETE needs another
	}
	pick := func(prefix string, val any) (string, bool) {
		if strings.HasPrefix(tmpl, prefix) {
			return pathVarRe.ReplaceAllString(tmpl, fmt.Sprint(val)), true
		}
		return "", false
	}
	for _, c := range []struct {
		prefix string
		val    any
	}{
		{"/v1/list/groups/", ids.Group},
		{"/v1/list/suggestions/", ids.Suggestion},
		{"/v1/list/", ids.Item},
		{"/v1/receipts/", ids.Receipt},
		{"/v1/lines/", ids.Line},
		{"/v1/stats/products/", ids.Product},
		{"/v1/products/", ids.Product},
		{"/v1/aliases/", ids.Alias},
		{"/v1/product-merges/", ids.ProductMerge},
		{"/v1/barcodes/", ids.Barcode},
		{"/v1/shops/", ids.Shop},
		{"/v1/categories/", ids.Category},
		{"/v1/product-groups/", ids.ProductGroup},
		{"/v1/product-group-ops/", ids.ProductGroupOp},
		{"/v1/auth/devices/", ids.Device},
		{"/v1/glossary/", ids.Word},
	} {
		if p, ok := pick(c.prefix, c.val); ok {
			return p
		}
	}
	panic("no id mapping for " + tmpl)
}

// report collects "not isolated" findings; the output doubles as the refactor checklist.
type report struct {
	lines map[string]bool
}

func newReport() *report { return &report{lines: map[string]bool{}} }

func (r *report) add(format string, a ...any) { r.lines[fmt.Sprintf(format, a...)] = true }

func (r *report) fail(t *testing.T, what string) {
	t.Helper()
	if len(r.lines) == 0 {
		return
	}
	var all []string
	for l := range r.lines {
		all = append(all, "  not isolated: "+l)
	}
	sort.Strings(all)
	t.Errorf("%s: %d problems\n%s", what, len(all), strings.Join(all, "\n"))
}

func is2xx(code int) bool { return code >= 200 && code < 300 }

func containsCode(codes []int, c int) bool {
	for _, x := range codes {
		if x == c {
			return true
		}
	}
	return false
}

// leaks reports whether a response body shows the other household's data.
func leaks(body string, v *household) bool {
	b := strings.ToLower(body)
	return strings.Contains(b, v.marker)
}

var _ = http.StatusOK

// strayIDs returns the ids in a list answer that are not in the caller's own set.
func strayIDs(body string, own map[int64]bool) []int64 {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		var wrapped struct {
			Items []map[string]any `json:"items"`
		}
		if json.Unmarshal([]byte(body), &wrapped) != nil {
			return nil
		}
		rows = wrapped.Items
	}
	var out []int64
	for _, r := range rows {
		if id := isoID(r); id != 0 && !own[id] {
			out = append(out, id)
		}
	}
	return out
}

// isHostedPath: routes that exist only in the hosted service (account, household, billing, Sign in with
// Apple). The household Server answers them 404 hosted_only; the hosted API is tested in hostedapi.
func isHostedPath(p string) bool {
	for _, pre := range []string{"/v1/account", "/v1/household", "/v1/billing", "/v1/auth/apple"} {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}
