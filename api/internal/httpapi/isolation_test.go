//go:build isolation

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// call performs the probe for op as caller, aimed at the ids of target.
func (p probe) call(op specOp, caller, target *household) *httptest.ResponseRecorder {
	path := pathFor(op.Method, op.Tmpl, target.ids)
	if p.Query != nil {
		path += p.Query(target, caller)
	}
	var body any
	if p.Body != nil {
		body = p.Body(target, caller)
	} else if op.Method == "POST" || op.Method == "PUT" || op.Method == "PATCH" {
		body = M{}
	}
	return caller.do(op.Method, path, body)
}

func (p probe) leakedBy(rec *httptest.ResponseRecorder, v *household) bool {
	body := rec.Body.String()
	if p.Echo {
		return strings.Contains(strings.ToLower(body), "english "+v.marker)
	}
	return leaks(body, v)
}

// TestIsolationProbesWorkForTheOwner is the control: every probe, called by the household that owns
// the ids, must succeed. It proves the isolation probes below are real requests (valid bodies, valid
// ids), so a 404 for the other household means "not yours" and not "bad request".
func TestIsolationProbesWorkForTheOwner(t *testing.T) {
	ops := specOps(t)
	shared := bootWorld(t)
	var bad []string
	for _, op := range ops {
		p, ok := probes[op.key()]
		if !ok || p.Exempt != "" {
			continue
		}
		w := shared
		if op.Method != "GET" {
			w = bootWorld(t) // a mutation may delete what the next probe needs
		}
		rec := p.call(op, w.a, w.a)
		t.Logf("owner %s -> %d", op.key(), rec.Code)
		ok2 := is2xx(rec.Code)
		if len(p.Ctrl) > 0 {
			ok2 = containsCode(p.Ctrl, rec.Code)
		}
		if ok2 && p.Own && !strings.Contains(strings.ToLower(rec.Body.String()), w.a.marker) {
			ok2 = false
			rec.Body.WriteString(" [own data missing from the answer]")
		}
		if !ok2 {
			bad = append(bad, fmt.Sprintf("%s: owner got %d %s", op.key(), rec.Code, truncate(rec.Body.String(), 120)))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("probes that do not work for the owner (fix the probe table):\n  %s", strings.Join(bad, "\n  "))
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

var _ = json.Marshal
var _ = http.StatusOK

// ---------------------------------------------------------------- the endpoint matrix

func sortedOps(ops []specOp) []specOp {
	rank := func(m string) int {
		switch m {
		case "GET":
			return 0
		case "DELETE":
			return 2
		}
		return 1
	}
	out := append([]specOp(nil), ops...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Method) < rank(out[j].Method) })
	return out
}

// setupAmbiguity: the victim's ids must not exist in the attacker's database, otherwise a hit could be
// the attacker's own row with the same number.
func setupAmbiguity(w *world, rep *report) {
	v, x := w.a, w.b
	if v.conn == x.conn {
		return // one shared database: ids are shared too, nothing to be ambiguous about
	}
	for name, c := range map[string]struct {
		id  int64
		set map[int64]bool
	}{
		"receipt": {v.ids.Receipt, x.ids.ReceiptIDs}, "product": {v.ids.Product, x.ids.ProductIDs},
		"shop": {v.ids.Shop, x.ids.ShopIDs}, "list item": {v.ids.Item, x.ids.ItemIDs}, "list group": {v.ids.Group, x.ids.GroupIDs},
	} {
		if c.set[c.id] {
			rep.add("test setup: A's %s id %d also exists in B, so a hit would be ambiguous (the seed must give A higher ids than B)", name, c.id)
		}
	}
}

// TestIsolationEndpoints calls every operation of openapi.yaml as household B with household A's ids.
func TestIsolationEndpoints(t *testing.T) {
	w := bootWorld(t)
	rep := newReport()
	setupAmbiguity(w, rep)
	before := w.a.tableDigests()

	for _, op := range sortedOps(specOps(t)) {
		p, ok := probes[op.key()]
		if !ok {
			rep.add("%s: no probe in the isolation matrix (add it to probes in isolation_probes_test.go, or exempt it with a reason)", op.key())
			continue
		}
		if p.Exempt != "" {
			continue
		}
		rec := p.call(op, w.b, w.a)
		if p.leakedBy(rec, w.a) {
			rep.add("%s: B received A's data (%d %s)", op.key(), rec.Code, truncate(rec.Body.String(), 80))
		}
		if !p.Scoped && rec.Code != http.StatusNotFound {
			rep.add("%s: B got %d for A's id, want 404", op.key(), rec.Code)
		}
		if p.Own && !strings.Contains(strings.ToLower(rec.Body.String()), w.b.marker) {
			rep.add("%s: B's own data is missing from the answer (%d)", op.key(), rec.Code)
		}
		if p.IDs != nil {
			if stray := strayIDs(rec.Body.String(), p.IDs(w.b)); len(stray) > 0 {
				rep.add("%s: B's list contains %d rows that are not B's (ids %v)", op.key(), len(stray), stray)
			}
		}
	}

	w.b.collectIDs()
	setupAmbiguity(w, rep) // again: B's own probes created rows too
	after := w.a.tableDigests()
	for tb, d := range before {
		if after[tb] != d {
			rep.add("table %s of household A was changed by B's requests (%s -> %s)", tb, d, after[tb])
		}
	}
	rep.fail(t, "endpoints, world: "+w.mode)
}

// ---------------------------------------------------------------- foreign ids in the attacker's own requests

// TestIsolationForeignReferences: B works on its own data but names an id of A inside the request
// (a product to link, a group to move into, a shop to assign). Every such write must be refused.
func TestIsolationForeignReferences(t *testing.T) {
	w := bootWorld(t)
	a, b := w.a, w.b
	rep := newReport()
	before := a.tableDigests()
	type refCase struct {
		name, method, path string
		body               any
	}
	cases := []refCase{
		{"link B's line to A's product", "PUT", fmt.Sprintf("/v1/lines/%d/product", b.ids.Line), M{"product_id": a.ids.Product}},
		{"list item with A's product", "POST", "/v1/list", M{"text": "x " + b.marker, "product_id": a.ids.Product}},
		{"list item into A's group", "POST", "/v1/list", M{"text": "x " + b.marker, "group_id": a.ids.Group}},
		{"move B's item into A's group", "POST", fmt.Sprintf("/v1/list/%d/move", b.ids.Item), M{"group_id": a.ids.Group}},
		{"assign B's receipt to A's shop", "PATCH", fmt.Sprintf("/v1/receipts/%d", b.ids.Receipt), M{"shop_id": a.ids.Shop}},
		{"upload a receipt for A's shop", "POST", "/v1/receipts", multipartImage("image", pngBytes(220), map[string]string{"shop_id": fmt.Sprint(a.ids.Shop)})},
		{"product in A's category", "POST", "/v1/products", M{"name": "p " + b.marker, "category_id": a.ids.Category}},
		{"move B's product to A's category", "PATCH", fmt.Sprintf("/v1/products/%d", b.ids.Product), M{"category_id": a.ids.Category}},
		{"merge B's product into A's", "POST", fmt.Sprintf("/v1/products/%d/merge", b.ids.Product), M{"into_product_id": a.ids.Product}},
		{"product group of A's products", "POST", "/v1/product-groups", M{"product_ids": []int64{a.ids.Product, a.ids.Product2}}},
		{"product group mixing B's and A's", "POST", "/v1/product-groups", M{"product_ids": []int64{b.ids.Product, a.ids.Product}}},
		{"deny pair of A's products", "POST", "/v1/product-groups/deny", M{"product_a_id": a.ids.Product, "product_b_id": a.ids.Product2}},
		{"merge B's product group into A's", "POST", fmt.Sprintf("/v1/product-groups/%d/merge", b.ids.ProductGroup), M{"into_group_id": a.ids.ProductGroup}},
		{"group item with A's product", "POST", fmt.Sprintf("/v1/list/groups/%d/items", b.ids.Group), M{"items": []any{M{"text": "x " + b.marker, "product": a.ids.Product, "product_id": a.ids.Product}}}},
		{"new group whose item has A's product", "POST", "/v1/list/groups", M{"name": "g " + b.marker, "items": []any{M{"text": "x", "product_id": a.ids.Product}}}},
		{"line under A's line", "POST", fmt.Sprintf("/v1/receipts/%d/lines", b.ids.Receipt), M{"raw_text": "x " + b.marker, "kind": "item", "line_total_cents": 1, "parent_line_id": a.ids.Line}},
	}
	for _, c := range cases {
		rec := b.do(c.method, c.path, c.body)
		if is2xx(rec.Code) {
			rep.add("%s %s (%s): accepted a reference to A's data (%d)", c.method, strings.SplitN(c.path, "?", 2)[0], c.name, rec.Code)
		}
		if leaks(rec.Body.String(), a) {
			rep.add("%s %s (%s): the answer contains A's data", c.method, strings.SplitN(c.path, "?", 2)[0], c.name)
		}
	}
	// Filters naming A's ids must find nothing.
	for _, q := range []string{
		fmt.Sprintf("/v1/receipts?shop_id=%d", a.ids.Shop),
		fmt.Sprintf("/v1/stats/spend?group_by=shop&shop=%d&from=2026-01-01&to=2026-12-31", a.ids.Shop),
		fmt.Sprintf("/v1/list?group_id=%d", a.ids.Group),
	} {
		rec := b.do("GET", q, nil)
		if leaks(rec.Body.String(), a) {
			rep.add("GET %s: the answer contains A's data", strings.SplitN(q, "?", 2)[0])
		}
	}
	after := a.tableDigests()
	for tb, d := range before {
		if after[tb] != d {
			rep.add("table %s of household A was changed by B's references (%s -> %s)", tb, d, after[tb])
		}
	}
	rep.fail(t, "foreign references, world: "+w.mode)
}

// ---------------------------------------------------------------- the same keys in both households

// TestIsolationSameKeysInBothHouseholds: unique constraints, idempotency keys and the glossary are per
// household. B must be able to reuse A's barcode, category name, image, client id and taught word without
// a conflict, without getting A's rows back and without changing A.
func TestIsolationSameKeysInBothHouseholds(t *testing.T) {
	w := bootWorld(t)
	a, b := w.a, w.b
	rep := newReport()
	before := a.tableDigests()

	if rec := b.do("POST", "/v1/products", M{"name": "Own product " + b.marker, "barcode": a.ids.Barcode}); rec.Code != 201 {
		rep.add("POST /v1/products: B cannot use a barcode A already has (%d): barcodes are unique across households", rec.Code)
	}
	if rec := b.do("POST", "/v1/categories", M{"name": a.ids.CategoryName}); rec.Code != 201 {
		rep.add("POST /v1/categories: B cannot use a category name A already has (%d): category names are unique across households", rec.Code)
	}
	if rec := b.do("POST", "/v1/shops", M{"name": "Shop " + b.marker, "domain": a.marker + "1.nl"}); rec.Code != 201 {
		rep.add("POST /v1/shops: B cannot use a shop domain A already has (%d)", rec.Code)
	}
	if rec := b.do("POST", "/v1/list", M{"text": "mine " + b.marker, "client_id": a.ids.ClientItem}); rec.Code != 201 || leaks(rec.Body.String(), a) || !strings.Contains(rec.Body.String(), "mine "+b.marker) {
		rep.add("POST /v1/list: B's client_id equal to A's returns A's item or fails (%d %s)", rec.Code, truncate(rec.Body.String(), 80))
	}
	if rec := b.do("POST", "/v1/list/groups", M{"name": "mine " + b.marker, "client_id": a.ids.ClientGroup}); rec.Code != 201 || leaks(rec.Body.String(), a) {
		rep.add("POST /v1/list/groups: B's client_id equal to A's returns A's group or fails (%d %s)", rec.Code, truncate(rec.Body.String(), 80))
	}
	// The same receipt photo in both households.
	b.w.ocr.rows.Store([]string{"01-09-2026 10:00|", "MELK " + strings.ToUpper(b.marker) + "|1,00", "TOTAAL|1,00"})
	rec := b.do("POST", "/v1/receipts", multipartImage("image", pngBytes(30), nil)) // A's key receipt was uploaded as pngBytes(30)
	b.drain()
	if rec.Code != http.StatusAccepted || leaks(rec.Body.String(), a) {
		rep.add("POST /v1/receipts: the same photo A uploaded cannot be uploaded by B, or the answer shows A's data (%d %s)", rec.Code, truncate(rec.Body.String(), 80))
	} else {
		var up M
		_ = json.Unmarshal(rec.Body.Bytes(), &up)
		got := b.do("GET", fmt.Sprintf("/v1/receipts/%d", int64(up["id"].(float64))), nil)
		if got.Code != 200 || leaks(got.Body.String(), a) {
			rep.add("GET /v1/receipts/{id}: B's copy of A's photo shows A's data (%d)", got.Code)
		}
	}

	// A word taught in A changes A's product names only.
	a.do("PUT", "/v1/glossary/blorptaart", M{"english": "blorp pie " + a.marker})
	pa := a.do("POST", "/v1/products", M{"name": "Blorptaart"})
	pb := b.do("POST", "/v1/products", M{"name": "Blorptaart"})
	if pa.Code != 201 || pb.Code != 201 {
		t.Fatalf("setup: products %d %d", pa.Code, pb.Code)
	}
	if !strings.Contains(strings.ToLower(pa.Body.String()), "blorp pie") {
		t.Fatalf("setup: A's taught word did not apply to A's own product: %s", pa.Body)
	}
	if strings.Contains(strings.ToLower(pb.Body.String()), "blorp pie") {
		rep.add("glossary: a word taught in A changed the English name of B's new product (process-wide glossary map)")
	}
	if rec := b.do("GET", "/v1/translate?text=blorptaart", nil); strings.Contains(strings.ToLower(rec.Body.String()), "blorp pie") {
		rep.add("GET /v1/translate: B sees the translation A taught (process-wide glossary map)")
	}
	if rec := b.do("GET", "/v1/glossary", nil); strings.Contains(strings.ToLower(rec.Body.String()), "blorptaart") {
		rep.add("GET /v1/glossary: B lists the word A taught")
	}
	// ... and the other way round: B teaching the same word differently does not change A.
	b.do("PUT", "/v1/glossary/blorptaart", M{"english": "blorp cake " + b.marker})
	if rec := a.do("GET", "/v1/translate?text=blorptaart", nil); !strings.Contains(strings.ToLower(rec.Body.String()), "blorp pie") {
		rep.add("glossary: B teaching the same word replaced A's meaning (A now gets %s)", truncate(rec.Body.String(), 80))
	}

	// Settings are per household.
	b.do("PATCH", "/v1/list/plan/settings", M{"max_stops": 1})
	if rec := a.do("GET", "/v1/list/plan/settings", nil); !strings.Contains(rec.Body.String(), `"max_stops":3`) {
		rep.add("settings: B changing its plan settings changed A's (%s)", truncate(rec.Body.String(), 80))
	}
	_ = before
	rep.fail(t, "same keys, world: "+w.mode)
}

// ---------------------------------------------------------------- tokens, images on disk, deleting a household

func TestIsolationTokensImagesAndErasure(t *testing.T) {
	w := bootWorld(t)
	a, b := w.a, w.b
	rep := newReport()

	// A token nobody issued is refused, and one household's token is not accepted as a stranger's.
	stranger := &household{name: "?", marker: "?", token: "st_" + strings.Repeat("a", 32), w: w, handler: a.handler}
	if rec := stranger.do("GET", "/v1/shops", nil); rec.Code != http.StatusUnauthorized {
		rep.add("auth: an unknown token got %d, want 401", rec.Code)
	}
	// Images of the two households are in different folders.
	if a.imagesDir != "" && a.imagesDir == b.imagesDir {
		rep.add("images: A and B share one image directory (%s); erasing one household would erase the other", a.imagesDir)
	}
	if a.imagesDir == "" || b.imagesDir == "" {
		rep.add("images: the world does not expose a per-household image directory (every household needs its own, so one can be erased)")
	}
	// Revoking A's device does not touch B.
	b.must("GET", "/v1/auth/me", nil, 200)
	a.do("DELETE", fmt.Sprintf("/v1/auth/devices/%d", a.ids.Device), nil)
	if rec := b.do("GET", "/v1/auth/devices", nil); !strings.Contains(rec.Body.String(), "iPhone "+b.marker) {
		rep.add("devices: revoking A's device removed B's (%s)", truncate(rec.Body.String(), 80))
	}
	// Deleting household A leaves household B intact, and A's token stops working.
	beforeB := b.tableDigests()
	if w.erase == nil {
		rep.add("erase: one household cannot be deleted on its own (one shared database, one image folder)")
	} else {
		if err := w.erase(a); err != nil {
			rep.add("erase: deleting household A failed: %v", err)
		}
		if rec := a.do("GET", "/v1/shops", nil); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
			rep.add("erase: A's token still works after A was deleted (%d)", rec.Code)
		}
		if rec := b.do("GET", "/v1/shops", nil); rec.Code != 200 || !strings.Contains(strings.ToLower(rec.Body.String()), b.marker) {
			rep.add("erase: B lost data when A was deleted (%d)", rec.Code)
		}
		afterB := b.tableDigests()
		for tb, d := range beforeB {
			if afterB[tb] != d {
				rep.add("erase: table %s of household B changed when A was deleted", tb)
			}
		}
	}
	rep.fail(t, "tokens, images, erasure, world: "+w.mode)
}
