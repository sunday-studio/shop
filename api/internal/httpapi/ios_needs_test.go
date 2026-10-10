package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"shop/internal/apitypes"
)

// Backend needs raised by the iOS app (docs/IOS-BACKEND-NEEDS.md).

func TestReceiptCountsAndLineProducts(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	var zuivel int64
	for _, c := range decodeAs[[]apitypes.Category](t, e.do("GET", "/v1/categories", nil)) {
		if c.Name == "Zuivel" {
			zuivel = c.Id
		}
	}
	milk := e.product(map[string]any{"name": "Halfvolle melk", "category_id": zuivel})
	r := e.receipt(shop.Id, 1, "01-10-2026|", "AH HALFV MELK|1,29", "KAAS|4,99", "BROOD|2,49", "BONUS KAAS|-0,50", "TOTAAL|8,27")
	e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", (*r.Lines)[0].Id), map[string]any{"product_id": milk.Id})
	e.receipt(shop.Id, 2, "MELK|1,29", "TOTAAL|9,99") // needs_review (no date, mismatch)

	// #1: item and unlinked counts on list rows (the discount isn't an item).
	page := decodeAs[apitypes.ReceiptPage](t, e.do("GET", "/v1/receipts", nil))
	first := page.Items[1]
	if first.LineCount == nil || *first.LineCount != 3 || *first.UnlinkedCount != 2 {
		t.Errorf("counts: %v %v", first.LineCount, first.UnlinkedCount)
	}
	// #2: receipts per status.
	c := decodeAs[map[string]int](t, e.do("GET", "/v1/receipts/counts", nil))
	if c["ok"] != 1 || c["needs_review"] != 1 || c["failed"] != 0 || c["total"] != 2 {
		t.Errorf("status counts: %v", c)
	}
	// #8: product name + category on lines.
	got := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil))
	l := (*got.Lines)[0]
	if l.ProductName == nil || *l.ProductName != "Halfvolle melk" || l.CategoryId == nil || *l.CategoryId != zuivel ||
		l.ProductNameEn == nil || *l.ProductNameEn != "Semi-skimmed milk" {
		t.Errorf("line product: %+v", l)
	}
	if (*got.Lines)[1].ProductName != nil || *got.LineCount != 3 || *got.UnlinkedCount != 2 {
		t.Errorf("unlinked line / single-receipt counts: %+v %v", (*got.Lines)[1], got.LineCount)
	}
}

func TestUnifiedSearch(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Halfvolle melk"})
	r := e.receipt(shop.Id, 1, "01-10-2026|", "AH HALFV MELK|1,29", "KAAS|4,99", "TOTAAL|6,28")
	e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", (*r.Lines)[0].Id), map[string]any{"product_id": milk.Id})
	e.receipt(shop.Id, 2, "02-10-2026|", "KAAS|4,99", "TOTAAL|4,99")

	type results struct {
		Products []apitypes.Product `json:"products"`
		Aliases  []struct {
			Text        string `json:"text"`
			ProductName string `json:"product_name"`
			ShopName    string `json:"shop_name"`
		} `json:"aliases"`
		Receipts []struct {
			Receipt      apitypes.Receipt `json:"receipt"`
			MatchedLines []string         `json:"matched_lines"`
		} `json:"receipts"`
	}
	s := decodeAs[results](t, e.do("GET", "/v1/search?q="+url.QueryEscape("halfv melk"), nil))
	if len(s.Products) == 0 || s.Products[0].Id != milk.Id {
		t.Errorf("products: %+v", s.Products)
	}
	if len(s.Aliases) != 1 || s.Aliases[0].Text != "ah halfvol melk" || s.Aliases[0].ShopName != "AH" {
		t.Errorf("aliases: %+v", s.Aliases)
	}
	if len(s.Receipts) != 1 || s.Receipts[0].Receipt.Id != r.Id || s.Receipts[0].MatchedLines[0] != "AH HALFV MELK" {
		t.Errorf("receipts: %+v", s.Receipts)
	}
	// English query finds the receipt through the linked product.
	en := decodeAs[results](t, e.do("GET", "/v1/search?q="+url.QueryEscape("semi-skimmed milk"), nil))
	if len(en.Receipts) != 1 || en.Receipts[0].Receipt.Id != r.Id {
		t.Errorf("english → receipt: %+v", en.Receipts)
	}
	// Line text match across receipts, newest first, without a product.
	kaas := decodeAs[results](t, e.do("GET", "/v1/search?q=kaas", nil))
	if len(kaas.Receipts) != 2 || kaas.Receipts[0].Receipt.Id <= kaas.Receipts[1].Receipt.Id {
		t.Errorf("kaas receipts: %+v", kaas.Receipts)
	}
	if rec := e.do("GET", "/v1/search?q=", nil); rec.Code != 400 {
		t.Errorf("empty q: %d", rec.Code)
	}
}

func TestIdempotentListAdds(t *testing.T) {
	e := newEnv(t)
	milk := e.product(map[string]any{"name": "Halfvolle melk"})
	add := func(body map[string]any, header string) (*httptest.ResponseRecorder, apitypes.ListItem) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/v1/list", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+e.token)
		if header != "" {
			req.Header.Set("Idempotency-Key", header)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		var it apitypes.ListItem
		_ = json.Unmarshal(rec.Body.Bytes(), &it)
		return rec, it
	}

	rec, first := add(map[string]any{"text": "eieren", "client_id": "c-1"}, "")
	if rec.Code != 201 {
		t.Fatalf("first: %d", rec.Code)
	}
	rec, again := add(map[string]any{"text": "eieren", "client_id": "c-1"}, "")
	if rec.Code != 200 || again.Id != first.Id {
		t.Errorf("replay: %d %+v", rec.Code, again)
	}

	// Merge path: a retried add of an open product must not bump qty twice.
	add(map[string]any{"product_id": milk.Id}, "")
	_, m1 := add(map[string]any{"product_id": milk.Id, "qty": 2}, "k-9")
	_, m2 := add(map[string]any{"product_id": milk.Id, "qty": 2}, "k-9")
	if m1.Qty == nil || *m1.Qty != 3 || *m2.Qty != 3 || m2.Id != m1.Id {
		t.Errorf("merge retried: %v %v", m1.Qty, m2.Qty)
	}

	// Replaying an add whose item was deleted → 410.
	e.do("DELETE", fmt.Sprintf("/v1/list/%d", first.Id), nil)
	if rec, _ := add(map[string]any{"text": "eieren", "client_id": "c-1"}, ""); rec.Code != http.StatusGone {
		t.Errorf("replay deleted: %d", rec.Code)
	}
}

func TestListChanges(t *testing.T) {
	e := newEnv(t)
	type changes struct {
		Items      []apitypes.ListItem `json:"items"`
		DeletedIDs []int64             `json:"deleted_ids"`
		ServerTime time.Time           `json:"server_time"`
		FullResync bool                `json:"full_resync"`
	}
	sync := func(since time.Time) changes {
		rec := e.do("GET", "/v1/list/changes?since="+url.QueryEscape(since.UTC().Format(time.RFC3339)), nil)
		if rec.Code != 200 {
			t.Fatalf("changes: %d %s", rec.Code, rec.Body)
		}
		return decodeAs[changes](t, rec)
	}
	t0 := time.Now().Add(-time.Minute)
	a := decodeAs[apitypes.ListItem](t, e.do("POST", "/v1/list", map[string]any{"text": "eieren"}))
	b := decodeAs[apitypes.ListItem](t, e.do("POST", "/v1/list", map[string]any{"text": "wc papier"}))
	if a.UpdatedAt == nil {
		t.Fatal("updated_at missing on list items")
	}
	c1 := sync(t0)
	if len(c1.Items) != 2 || len(c1.DeletedIDs) != 0 || c1.FullResync {
		t.Fatalf("initial: %+v", c1)
	}

	// Check one, delete the other; both show up since the last sync point.
	e.do("POST", fmt.Sprintf("/v1/list/%d/check", a.Id), nil)
	e.do("DELETE", fmt.Sprintf("/v1/list/%d", b.Id), nil)
	c2 := sync(c1.ServerTime)
	if len(c2.Items) != 1 || c2.Items[0].Id != a.Id || c2.Items[0].CheckedAt == nil {
		t.Errorf("checked item: %+v", c2.Items)
	}
	if len(c2.DeletedIDs) != 1 || c2.DeletedIDs[0] != b.Id {
		t.Errorf("deleted: %+v", c2.DeletedIDs)
	}
	// Clearing checked items records tombstones too. (The boundary is
	// inclusive at second precision, so b may repeat; that's harmless.)
	e.do("DELETE", "/v1/list/checked", nil)
	if c3 := sync(c2.ServerTime); !slices.Contains(c3.DeletedIDs, a.Id) {
		t.Errorf("clear checked: %+v", c3.DeletedIDs)
	}
	if old := sync(time.Now().Add(-40 * 24 * time.Hour)); !old.FullResync {
		t.Error("since older than the tombstone window must ask for a full resync")
	}
	if rec := e.do("GET", "/v1/list/changes?since=yesterday", nil); rec.Code != 400 {
		t.Errorf("bad since: %d", rec.Code)
	}
}
