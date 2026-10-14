//go:build isolation

package httpapi_test

// The isolation harness: two households, A and B, each with data in every table a household owns.
//
// A "world" is the system under test. bootWorld builds it. When the server is single-tenant (one
// database for everything) both households are tokens in the same database, which is exactly what
// today's code can do; the isolation tests then fail with "not isolated: ..." lines, the refactor
// checklist. When the tenant seam exists, bootWorld builds two real households with their own
// databases and the same tests must pass.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"shop/internal/store/db"
)

// oddWords are long words no glossary knows: a receipt line starting with one makes an "odd name" product
// that waits for review. A different one per seed round keeps the rounds from linking to each other.
var oddWords = []string{"ZOEPLAKUNDIG", "KRANTENBOLLOR", "WIMPELSTROEPEN", "BLAFFERDORAMIT", "SNUIFTOBBEROL", "GRUNKELPLOEPS"}

// seedIDs are the ids of household data the other household will try to reach.
type seedIDs struct {
	Shop, Category, Receipt, Line, Product, Product2, Alias, Item, Group, Template                                 int64
	Suggestion, Review, TrashGroup, ProductGroup, ProductGroup2, GroupMember, ProductGroupOp, ProductMerge, Device int64
	Barcode, Word, Word2, Image, ClientItem, ClientGroup, CategoryName                                             string
	ReceiptTotal                                                                                                   int64
	ReceiptIDs, ProductIDs, ShopIDs, ItemIDs, GroupIDs                                                             map[int64]bool
}

type household struct {
	name   string // "A" or "B"
	marker string // a string that appears in every piece of this household's data, lower case
	token  string
	w      *world
	conn   *sql.DB // the household's own database (shared with the other one in a single-tenant world)
	ids    seedIDs
	// handler serves this household's requests. In a single-tenant world both households use the same one.
	handler http.Handler
	drain   func() // runs this household's queued jobs
	// imagesDir is the household's own folder of receipt and product images.
	imagesDir string
}

type world struct {
	t    *testing.T
	a, b *household
	ocr  *fakeOCR
	// erase removes one household completely (database, images, backups). Nil when the system
	// cannot do that for one household alone.
	erase func(h *household) error
	// mode is a label for the failure output.
	mode string
	// bytes of the PNG used for the "same image in both households" check
	closers []func()
}

func (h *household) do(method, path string, body any) *httptest.ResponseRecorder {
	h.w.t.Helper()
	var rd io.Reader
	ct := ""
	switch v := body.(type) {
	case nil:
	case rawBody:
		rd, ct = bytes.NewReader(v.data), v.contentType
	default:
		b, _ := json.Marshal(body)
		rd, ct = bytes.NewReader(b), "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

type rawBody struct {
	contentType string
	data        []byte
}

// multipartImage is a multipart body with one image file field (and optional text fields).
func multipartImage(field string, img []byte, fields map[string]string) rawBody {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(field, "x.png")
	_, _ = fw.Write(img)
	_ = mw.Close()
	return rawBody{contentType: mw.FormDataContentType(), data: b.Bytes()}
}

// must performs a request that has to succeed (a seed step) and returns the decoded JSON object.
func (h *household) must(method, path string, body any, want int) map[string]any {
	h.w.t.Helper()
	rec := h.do(method, path, body)
	if rec.Code != want {
		h.w.t.Fatalf("seed %s: %s %s: %d %s (want %d)", h.name, method, path, rec.Code, rec.Body, want)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (h *household) mustList(path string) []map[string]any {
	h.w.t.Helper()
	rec := h.do("GET", path, nil)
	if rec.Code != 200 {
		h.w.t.Fatalf("seed %s: GET %s: %d %s", h.name, path, rec.Code, rec.Body)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		var wrapped struct {
			Items []map[string]any `json:"items"`
		}
		if err2 := json.Unmarshal(rec.Body.Bytes(), &wrapped); err2 != nil {
			h.w.t.Fatalf("seed %s: GET %s: not a list: %s", h.name, path, rec.Body)
		}
		out = wrapped.Items
	}
	return out
}

func isoID(m map[string]any) int64 {
	switch v := m["id"].(type) {
	case float64:
		return int64(v)
	}
	return 0
}

// receipt uploads a receipt image whose OCR text is rows ("left|right") and waits for it to be read.
func (h *household) receipt(shopID int64, seed uint8, rows ...string) map[string]any {
	h.w.t.Helper()
	h.w.ocr.rows.Store(rows)
	fields := map[string]string{}
	if shopID != 0 {
		fields["shop_id"] = fmt.Sprint(shopID)
	}
	rec := h.do("POST", "/v1/receipts", multipartImage("image", pngBytes(seed), fields))
	if rec.Code != http.StatusAccepted {
		h.w.t.Fatalf("seed %s: upload: %d %s", h.name, rec.Code, rec.Body)
	}
	var up map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &up)
	h.drain()
	return h.must("GET", fmt.Sprintf("/v1/receipts/%d", isoID(up)), nil, 200)
}

// seed fills the household with data in every area. rounds > 1 repeats it with other names, so
// this household's later ids are higher than any id of a household that seeded once.
func (h *household) seed(rounds int, imageBase uint8) {
	t := h.w.t
	t.Helper()
	m := h.marker
	amount := "1,29" // A and B use different amounts so spend totals differ
	total := "3,29"
	if h.name == "B" {
		amount, total = "2,49", "4,49"
	}
	for r := 1; r <= rounds; r++ {
		last := r == rounds
		tag := fmt.Sprintf("%s%d", m, r)
		shop := h.must("POST", "/v1/shops", map[string]any{"name": "Shop " + tag, "city": "Amsterdam", "chain": "Chain " + tag, "domain": tag + ".nl"}, 201)
		cat := h.must("POST", "/v1/categories", map[string]any{"name": "Cat " + tag}, 201)
		spare := h.must("POST", "/v1/categories", map[string]any{"name": "Spare " + tag}, 201)
		ean := func(n int) string {
			return fmt.Sprintf("87100%s%02d%02d", map[string]string{"A": "1", "B": "2"}[h.name], r, n) + "00"
		}
		p1 := h.must("POST", "/v1/products", map[string]any{"name": "Melk " + tag, "brand": "Brand " + tag, "barcode": ean(1), "category_id": isoID(cat), "net_quantity": 1000, "net_unit": "ml"}, 201)
		p2 := h.must("POST", "/v1/products", map[string]any{"name": "Brood " + tag, "barcode": ean(2)}, 201)
		p3 := h.must("POST", "/v1/products", map[string]any{"name": "Kaas " + tag, "barcode": ean(3)}, 201)
		p4 := h.must("POST", "/v1/products", map[string]any{"name": "Kaasje " + tag, "barcode": ean(4)}, 201)

		var rcp map[string]any
		for k := 0; k < 3; k++ {
			rcp = h.receipt(isoID(shop), imageBase+uint8(r*10+k),
				fmt.Sprintf("0%d-09-2026 10:00|", k+1), strings.ToUpper("Melk "+tag)+"|"+amount, strings.ToUpper("Brood "+tag)+"|2,00",
				"TOTAAL|"+total)
			if k == 0 {
				// the first receipt is the one the other household will try to reach
				h.ids.Receipt = isoID(rcp)
			}
		}
		_ = rcp
		first := h.must("GET", fmt.Sprintf("/v1/receipts/%d", h.ids.Receipt), nil, 200)
		lines, _ := first["lines"].([]any)
		if len(lines) < 2 {
			t.Fatalf("seed %s: receipt has %d lines", h.name, len(lines))
		}
		l1 := int64(lines[0].(map[string]any)["id"].(float64))
		l2 := int64(lines[1].(map[string]any)["id"].(float64))
		h.must("PUT", fmt.Sprintf("/v1/lines/%d/product", l1), map[string]any{"product_id": isoID(p1)}, 200)
		h.must("PUT", fmt.Sprintf("/v1/lines/%d/product", l2), map[string]any{"product_id": isoID(p2)}, 200)

		item := h.must("POST", "/v1/list", map[string]any{"text": "Appels " + tag, "qty": 2, "note": "note " + tag, "client_id": "cid-item-" + tag}, 201)
		h.must("POST", "/v1/list", map[string]any{"text": "Peren " + tag}, 201)
		group := h.must("POST", "/v1/list/groups", map[string]any{"name": "Group " + tag, "client_id": "cid-group-" + tag, "items": []any{map[string]any{"text": "Ui " + tag}}}, 201)
		tmpl := h.must("POST", "/v1/list/groups", map[string]any{"name": "Template " + tag, "kind": "template", "items": []any{map[string]any{"text": "Rijst " + tag}}}, 201)

		pg := h.must("POST", "/v1/product-groups", map[string]any{"product_ids": []int64{isoID(p3), isoID(p4)}, "name": "Cheese " + tag}, 200)
		var pgID int64
		if g, ok := pg["group"].(map[string]any); ok {
			pgID = isoID(g)
		}
		opID := int64(pg["op_id"].(float64))
		var pg1ID = pgID
		g1 := []int64{}
		for i := 7; i <= 9; i++ {
			g1 = append(g1, isoID(h.must("POST", "/v1/products", map[string]any{"name": fmt.Sprintf("Fruit%d %s", i, tag)}, 201)))
		}
		pg3 := h.must("POST", "/v1/product-groups", map[string]any{"product_ids": g1, "name": "Fruit " + tag}, 200)
		pgID = isoID(pg3["group"].(map[string]any))

		// a merge, so there is something to undo
		p5 := h.must("POST", "/v1/products", map[string]any{"name": "Pasta " + tag}, 201)
		p6 := h.must("POST", "/v1/products", map[string]any{"name": "Pastasaus " + tag}, 201)
		merge := h.must("POST", fmt.Sprintf("/v1/products/%d/merge", isoID(p6)), map[string]any{"into_product_id": isoID(p5)}, 200)

		h.must("PUT", fmt.Sprintf("/v1/products/%d/image", isoID(p1)), multipartImage("image", pngBytes(uint8(150+r)), nil), 200)
		trash := h.must("POST", "/v1/list/groups", map[string]any{"name": "Trash " + tag}, 201)
		h.must("DELETE", fmt.Sprintf("/v1/list/groups/%d", isoID(trash)), nil, 200)
		// a "looks bought?" suggestion: a list item and a confirmed receipt that roughly matches it
		h.must("POST", "/v1/list", map[string]any{"text": "havermout bio " + tag}, 201)
		hr := h.receipt(isoID(shop), imageBase+uint8(r*10+8), today()+" 10:00|", "AH HAVERMOUT "+strings.ToUpper(tag)+"|2,10", oddWords[(r-1)%len(oddWords)]+" "+strings.ToUpper(tag)+"|1,00", "TOTAAL|3,10")
		conf := h.must("POST", fmt.Sprintf("/v1/receipts/%d/confirm", isoID(hr)), nil, 200)
		var sugID int64
		if l, ok := conf["list"].(map[string]any); ok {
			if sugs, ok := l["suggestions"].([]any); ok && len(sugs) > 0 {
				sugID = isoID(sugs[0].(map[string]any))
			}
		}
		if last && sugID == 0 {
			t.Fatalf("seed %s: no list suggestion was created: %v", h.name, conf["list"])
		}

		word := "zz" + strings.ToLower(tag) + "ww"
		h.must("PUT", "/v1/glossary/"+word, map[string]any{"english": "english " + tag}, 200)
		h.must("PUT", "/v1/glossary/"+word+"x", map[string]any{"english": "english2 " + tag}, 200)
		h.must("PATCH", "/v1/list/plan/settings", map[string]any{"max_stops": map[string]int{"A": 3, "B": 2}[h.name], "extra_stop_cents": 100 + r}, 200)

		var alias int64
		for _, a := range h.mustList(fmt.Sprintf("/v1/products/%d/aliases", isoID(p1))) {
			alias = isoID(a)
		}
		// A device token (the paired-phone kind), so the device list and revocation have something.
		d, err := db.New(h.conn).CreateToken(context.Background(), db.CreateTokenParams{
			Name: "iPhone " + tag, TokenHash: "devhash-" + tag, Kind: "device", CreatedAt: "2026-09-01T10:00:00Z"})
		if err != nil {
			t.Fatalf("seed %s: device: %v", h.name, err)
		}
		if !last {
			continue
		}
		h.ids.Device = d.ID
		h.ids.ClientItem, h.ids.ClientGroup = "cid-item-"+tag, "cid-group-"+tag
		h.ids.CategoryName = "Cat " + tag
		h.ids.Shop, h.ids.Category = isoID(shop), isoID(spare)
		h.ids.Line, h.ids.Product, h.ids.Product2 = l1, isoID(p1), isoID(p2)
		h.ids.Alias, h.ids.Item, h.ids.Group, h.ids.Template = alias, isoID(item), isoID(group), isoID(tmpl)
		h.ids.ProductGroup, h.ids.ProductGroupOp = pgID, opID
		h.ids.ProductGroup2, h.ids.GroupMember = pg1ID, g1[0]
		h.ids.ProductMerge = int64(merge["merge_id"].(float64))
		h.ids.Barcode, h.ids.Word, h.ids.Word2 = ean(1), word, word+"x"
		h.ids.Suggestion, h.ids.TrashGroup = sugID, isoID(trash)
		h.ids.ReceiptTotal = int64(first["total_cents"].(float64))
	}
	rq := h.must("GET", "/v1/products/review-queue", nil, 200)
	items, _ := rq["items"].([]any)
	for _, it := range items {
		pr := it.(map[string]any)["product"].(map[string]any)
		if strings.Contains(strings.ToLower(fmt.Sprint(pr["name"])), strings.ToLower(oddWords[(rounds-1)%len(oddWords)])) && isoID(pr) > h.ids.Review {
			h.ids.Review = isoID(pr) // the newest, so it is higher than any id of the other household
		}
	}
	if h.ids.Review == 0 {
		t.Fatalf("seed %s: no product is waiting for review: %v", h.name, rq["counts"])
	}
	h.collectIDs()
}

// collectIDs records every id the household owns, through its own API.
func (h *household) collectIDs() {
	h.ids.ReceiptIDs = idSet(h.mustList("/v1/receipts?limit=200"))
	h.ids.ProductIDs = idSet(h.mustList("/v1/products?limit=200"))
	h.ids.ShopIDs = idSet(h.mustList("/v1/shops"))
	h.ids.ItemIDs = idSet(h.mustList("/v1/list"))
	h.ids.GroupIDs = idSet(h.mustList("/v1/list/groups"))
}

func idSet(rows []map[string]any) map[int64]bool {
	s := map[int64]bool{}
	for _, r := range rows {
		s[isoID(r)] = true
	}
	return s
}

// tableDigests fingerprints every table of the household's database: row count and a hash of all
// rows. api_tokens is left out (last_used_at moves with every request) and so are SQLite internals.
func (h *household) tableDigests() map[string]string {
	t := h.w.t
	t.Helper()
	rows, err := h.conn.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'goose_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	out := map[string]string{}
	for _, tb := range tables {
		if tb == "api_tokens" || tb == "jobs" {
			continue
		}
		r, err := h.conn.Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			continue // virtual-table internals can refuse a plain scan
		}
		cols, _ := r.Columns()
		var lines []string
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = r.Scan(ptrs...)
			lines = append(lines, fmt.Sprint(vals...))
		}
		r.Close()
		sort.Strings(lines)
		sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
		out[tb] = fmt.Sprintf("%d rows, %s", len(lines), hex.EncodeToString(sum[:6]))
	}
	return out
}

func rowCount(d string) int {
	var n int
	fmt.Sscanf(d, "%d rows", &n)
	return n
}

// mcpSession connects an MCP client to the household's /mcp with its token.
func (h *household) mcpSession() (*mcp.ClientSession, error) {
	h.w.t.Helper()
	srv := httptest.NewServer(h.handler)
	h.w.t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "isolation", Version: "1"}, nil)
	return client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: h.token, base: http.DefaultTransport}},
		MaxRetries: -1,
	}, nil)
}

// bootWorld builds the system under test and seeds both households.
func bootWorld(t *testing.T) *world {
	t.Helper()
	var w *world
	if os.Getenv("ISOLATION_WORLD") == "shared" {
		// The control: two tokens in ONE database. Every check must fail here; if one passes, it tests nothing.
		w = bootSharedDatabaseWorld(t)
	} else {
		w = bootMultiTenantWorld(t)
	}
	// A seeds several rounds and keeps the ids of the last, so every id A is attacked with is higher than any id
	// B has even after B's own probes created rows (an id that also exists in B would make a hit ambiguous).
	w.a.seed(6, 10)
	w.b.seed(1, 100)
	return w
}
