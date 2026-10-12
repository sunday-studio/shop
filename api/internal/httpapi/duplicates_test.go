package httpapi_test

import (
	"fmt"
	"testing"
)

// Possible-duplicate warning: a newer receipt that looks like an older one (same printed total, same
// local purchase day, compatible shop). Computed on read; only "keep both" is stored.

func euros(c int) string { return fmt.Sprintf("%d,%02d", c/100, c%100) }

// basket builds receipt rows (first row = date) for the given item names with prices that sum to total.
func basket(date string, total int, names ...string) []string {
	rows := []string{date}
	per := total / len(names)
	sum := 0
	for i, n := range names {
		p := per
		if i == len(names)-1 {
			p = total - sum
		}
		sum += p
		rows = append(rows, n+"|"+euros(p))
	}
	return append(rows, "TOTAAL|"+euros(total))
}

var (
	groceriesA = []string{"HALFVOLLE MELK", "BANANEN", "BROOD VOLKOREN", "KAASSCHIJVEN", "PASTA PENNE", "TOMATEN", "KIPFILET", "YOGHURT VOL", "APPELS", "KOFFIE", "ROOMBOTER", "EIEREN 10"}
	groceriesB = []string{"PINDAKAAS", "RIJST", "SPINAZIE", "ZALM", "CHIPS", "BIER 6PK", "WASMIDDEL", "TONIJN", "SINAASAPPELSAP", "UIEN", "WORTELEN", "HAVERMOUT"}
)

// rcpt uploads a receipt (distinct image per seed) and runs OCR + parsing. shop 0 = unassigned.
func (e *env) rcpt(shop int64, seed uint8, rows []string) int64 {
	e.t.Helper()
	e.ocr.rows.Store(rows)
	rec := e.upload(shop, pngBytes(seed), nil)
	if rec.Code != 202 {
		e.t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	e.drain()
	return int64(jsonMap(e.t, rec.Body.String())["id"].(float64))
}

func (e *env) setHeader(id int64, total *int64, at *string) {
	e.t.Helper()
	if _, err := e.conn.Exec(`UPDATE receipts SET total_cents = ?, purchased_at = ? WHERE id = ?`, total, at, id); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) dupOf(id int64) map[string]any {
	e.t.Helper()
	rec := e.do("GET", fmt.Sprintf("/v1/receipts/%d", id), nil)
	if rec.Code != 200 {
		e.t.Fatalf("get %d: %d %s", id, rec.Code, rec.Body)
	}
	d, _ := jsonMap(e.t, rec.Body.String())["possible_duplicate_of"].(map[string]any)
	return d
}

func (e *env) dupCount() int {
	e.t.Helper()
	return int(jsonMap(e.t, e.do("GET", "/v1/receipts/counts", nil).Body.String())["possible_duplicates"].(float64))
}

func wantNoDup(t *testing.T, e *env, id int64, why string) {
	t.Helper()
	if d := e.dupOf(id); d != nil {
		t.Errorf("%s: receipt %d flagged: %v", why, id, d)
	}
}

func wantDup(t *testing.T, e *env, id, other int64, match string) map[string]any {
	t.Helper()
	d := e.dupOf(id)
	if d == nil {
		t.Fatalf("receipt %d not flagged, want %d (%s)", id, other, match)
	}
	if int64(d["receipt_id"].(float64)) != other || d["match"] != match {
		t.Fatalf("receipt %d: got %v, want receipt %d (%s)", id, d, other, match)
	}
	return d
}

// The two real pairs: receipt 4 is a re-photo of receipt 1 (8468) and receipt 8 of receipt 2 (9980).
func TestDuplicateRealPairs(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	r1 := e.rcpt(ah.Id, 1, basket("09-10-2026 18:42", 8468, groceriesA...))
	r2 := e.rcpt(ah.Id, 2, basket("08-10-2026 11:05", 9980, groceriesB...))
	other := e.rcpt(ah.Id, 3, basket("07-10-2026 09:00", 2350, "MELK", "BROOD", "KAAS"))
	r4 := e.rcpt(ah.Id, 4, basket("09-10-2026 18:42", 8468, groceriesA...)) // re-photo of 1
	r8 := e.rcpt(ah.Id, 8, basket("08-10-2026 11:05", 9980, groceriesB...)) // re-photo of 2

	d := wantDup(t, e, r4, r1, "total+date+lines")
	if d["total_cents"].(float64) != 8468 || d["shop_name"] != "Albert Heijn" || d["shop"].(map[string]any)["id"].(float64) != float64(ah.Id) ||
		d["purchased_at"] == nil || d["shared_lines"].(float64) != 12 || d["item_count"].(float64) != 12 || d["time_matches"] != true {
		t.Errorf("shape: %v", d)
	}
	wantDup(t, e, r8, r2, "total+date+lines")
	// Only the newer receipt of a pair carries the warning.
	for _, id := range []int64{r1, r2, other} {
		wantNoDup(t, e, id, "older or unrelated receipt")
	}
	if n := e.dupCount(); n != 2 {
		t.Errorf("possible_duplicates = %d, want 2", n)
	}
	// The list rows carry it too.
	page := jsonMap(t, e.do("GET", "/v1/receipts", nil).Body.String())["items"].([]any)
	flagged := map[int64]bool{}
	for _, it := range page {
		m := it.(map[string]any)
		if m["possible_duplicate_of"] != nil {
			flagged[int64(m["id"].(float64))] = true
		}
	}
	if len(flagged) != 2 || !flagged[r4] || !flagged[r8] {
		t.Errorf("list rows flagged = %v", flagged)
	}
}

func TestDuplicateFalsePositiveGuards(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	dirk := e.shop("Dirk")

	t.Run("same total and day, different content is only a guess", func(t *testing.T) {
		a := e.rcpt(ah.Id, 10, basket("05-10-2026 10:00", 6000, groceriesA...))
		b := e.rcpt(ah.Id, 11, basket("05-10-2026 17:30", 6000, groceriesB...))
		d := wantDup(t, e, b, a, "total+date")
		if d["shared_lines"].(float64) != 0 || d["time_matches"] != false {
			t.Errorf("%v", d)
		}
	})
	t.Run("small baskets need 3 items or shared lines", func(t *testing.T) {
		a := e.rcpt(ah.Id, 12, basket("06-10-2026 10:00", 450, "MELK", "BROOD"))
		b := e.rcpt(ah.Id, 13, basket("06-10-2026 16:00", 450, "KAAS", "BANAAN"))
		wantNoDup(t, e, b, "two different items under 10 euro")
		c := e.rcpt(ah.Id, 14, basket("06-10-2026 10:00", 450, "MELK", "BROOD")) // same two items
		wantDup(t, e, c, a, "total+date+lines")
		d1 := e.rcpt(dirk.Id, 15, basket("06-10-2026 10:00", 700, "A1", "A2", "A3"))
		d2 := e.rcpt(dirk.Id, 16, basket("06-10-2026 12:00", 700, "B1", "B2", "B3"))
		wantDup(t, e, d2, d1, "total+date") // 3 item lines each: allowed as a guess
	})
	t.Run("different day", func(t *testing.T) {
		a := e.rcpt(ah.Id, 17, basket("01-10-2026 10:00", 5555, groceriesA...))
		b := e.rcpt(ah.Id, 18, basket("02-10-2026 10:00", 5555, groceriesA...))
		wantNoDup(t, e, b, "other day")
		_ = a
	})
	t.Run("different total", func(t *testing.T) {
		e.rcpt(ah.Id, 19, basket("03-10-2026 10:00", 5555, groceriesA...))
		b := e.rcpt(ah.Id, 20, basket("03-10-2026 10:00", 5556, groceriesA...))
		wantNoDup(t, e, b, "one cent apart")
	})
	t.Run("different shops", func(t *testing.T) {
		e.rcpt(ah.Id, 21, basket("04-10-2026 10:00", 7777, groceriesA...))
		b := e.rcpt(dirk.Id, 22, basket("04-10-2026 10:00", 7777, groceriesA...))
		wantNoDup(t, e, b, "two assigned shops")
	})
	t.Run("no date, no total", func(t *testing.T) {
		a := e.rcpt(ah.Id, 23, basket("09-09-2026 10:00", 4321, groceriesA...))
		b := e.rcpt(ah.Id, 24, basket("09-09-2026 10:00", 4321, groceriesA...))
		wantDup(t, e, b, a, "total+date+lines")
		e.setHeader(b, nil, ptr("2026-09-09T08:00:00Z"))
		wantNoDup(t, e, b, "no total")
		e.setHeader(b, ptr(int64(4321)), nil)
		wantNoDup(t, e, b, "no date")
		e.setHeader(a, nil, ptr("2026-09-09T08:00:00Z"))
		e.setHeader(b, ptr(int64(4321)), ptr("2026-09-09T08:00:00Z"))
		wantNoDup(t, e, b, "older has no total")
		e.setHeader(a, ptr(int64(4321)), nil)
		wantNoDup(t, e, b, "older has no date")
	})
	t.Run("not parsed yet", func(t *testing.T) {
		a := e.rcpt(ah.Id, 25, basket("08-09-2026 10:00", 3210, groceriesA...))
		_ = a
		e.ocr.rows.Store(basket("08-09-2026 10:00", 3210, groceriesA...))
		rec := e.upload(ah.Id, pngBytes(26), nil) // not drained: still pending
		id := int64(jsonMap(t, rec.Body.String())["id"].(float64))
		wantNoDup(t, e, id, "pending receipt")
	})
}

func ptr[T any](v T) *T { return &v }

func TestDuplicateUnassignedReceipts(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	a := e.rcpt(0, 30, basket("05-10-2026 10:00", 6000, groceriesA...)) // unassigned older
	b := e.rcpt(ah.Id, 31, basket("05-10-2026 10:00", 6000, groceriesA...))
	d := wantDup(t, e, b, a, "total+date+lines")
	if d["shop"] != nil || d["shop_name"] != nil {
		t.Errorf("older receipt has no shop: %v", d)
	}
	c := e.rcpt(0, 32, basket("05-10-2026 10:00", 6000, groceriesA...)) // unassigned newer: matches the first strong one
	wantDup(t, e, c, a, "total+date+lines")
	if n := e.dupCount(); n != 2 {
		t.Errorf("count = %d (b and c; a is the oldest)", n)
	}
}

// The date-only purchased_at is local midnight (22:00Z the day before); the day is the Amsterdam day.
func TestDuplicateUsesLocalDay(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	rows := basket("09-10-2026 10:00", 5000, groceriesA...)
	a := e.rcpt(ah.Id, 40, rows)
	b := e.rcpt(ah.Id, 41, rows)
	c := e.rcpt(ah.Id, 42, rows)
	total := ptr(int64(5000))
	e.setHeader(a, total, ptr("2026-10-08T22:00:00Z")) // 9 Oct 00:00 local, date only
	e.setHeader(b, total, ptr("2026-10-09T21:59:00Z")) // 9 Oct 23:59 local
	e.setHeader(c, total, ptr("2026-10-09T22:00:00Z")) // 10 Oct 00:00 local: next day
	wantDup(t, e, b, a, "total+date+lines")
	wantNoDup(t, e, c, "midnight local belongs to the next day")
	d := e.dupOf(b)
	if d["time_matches"] != false {
		t.Errorf("date-only has no time: %v", d)
	}
	// Winter time (UTC+1): 23:30 local on 9 Jan is 22:30Z.
	e.setHeader(a, total, ptr("2027-01-08T23:00:00Z")) // 9 Jan 00:00 local
	e.setHeader(b, total, ptr("2027-01-09T22:30:00Z")) // 9 Jan 23:30 local
	e.setHeader(c, total, ptr("2027-01-09T23:00:00Z")) // 10 Jan 00:00 local
	wantDup(t, e, b, a, "total+date+lines")
	wantNoDup(t, e, c, "winter midnight")
}

func TestDuplicateStrongestOfSeveral(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	different := e.rcpt(ah.Id, 50, basket("05-10-2026 08:00", 6000, groceriesB...))
	same := e.rcpt(ah.Id, 51, basket("05-10-2026 18:00", 6000, groceriesA...))
	third := e.rcpt(ah.Id, 52, basket("05-10-2026 18:00", 6000, groceriesA...))
	wantDup(t, e, same, different, "total+date")
	wantDup(t, e, third, same, "total+date+lines") // lines beat the older date
	// Remove the copy it matched: the next one takes over; remove the last: no banner.
	if rec := e.do("DELETE", fmt.Sprintf("/v1/receipts/%d", same), nil); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	wantDup(t, e, third, different, "total+date")
	e.do("DELETE", fmt.Sprintf("/v1/receipts/%d", different), nil)
	wantNoDup(t, e, third, "other receipt gone")
	if e.dupCount() != 0 {
		t.Error("count after deletes")
	}
}

func TestDuplicateKeepBothIsRemembered(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	a := e.rcpt(ah.Id, 60, basket("05-10-2026 10:00", 6000, groceriesA...))
	b := e.rcpt(ah.Id, 61, basket("05-10-2026 10:00", 6000, groceriesA...))
	wantDup(t, e, b, a, "total+date+lines")

	url := fmt.Sprintf("/v1/receipts/%d/duplicate/keep", b)
	for i := 0; i < 2; i++ { // idempotent
		if rec := e.do("POST", url, nil); rec.Code != 204 {
			t.Fatalf("keep: %d %s", rec.Code, rec.Body)
		}
	}
	wantNoDup(t, e, b, "kept both")
	if e.dupCount() != 0 {
		t.Error("kept receipt still counted")
	}
	if n := count(t, e, `SELECT COUNT(*) FROM receipt_duplicate_dismissals`); n != 1 {
		t.Errorf("dismissal rows = %d", n)
	}
	// Nothing was deleted or changed.
	if n := count(t, e, `SELECT COUNT(*) FROM receipts`); n != 2 {
		t.Errorf("receipts = %d", n)
	}
	// Deleting the older one does not bring it back; undo clears the decision.
	for i := 0; i < 2; i++ {
		if rec := e.do("DELETE", url, nil); rec.Code != 204 {
			t.Fatalf("undo: %d", rec.Code)
		}
	}
	wantDup(t, e, b, a, "total+date+lines")
	if rec := e.do("POST", "/v1/receipts/9999/duplicate/keep", nil); rec.Code != 404 {
		t.Errorf("unknown receipt: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/v1/receipts/9999/duplicate/keep", nil); rec.Code != 404 {
		t.Errorf("unknown receipt undo: %d", rec.Code)
	}
	// Deleting the receipt takes the decision with it.
	e.do("POST", url, nil)
	e.do("DELETE", fmt.Sprintf("/v1/receipts/%d", b), nil)
	if n := count(t, e, `SELECT COUNT(*) FROM receipt_duplicate_dismissals`); n != 0 {
		t.Errorf("dismissal row outlived its receipt: %d", n)
	}
}

// A third receipt that matches later is its own newer receipt: a decision about one never hides another.
func TestDuplicateKeepBothIsPerReceipt(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn")
	a := e.rcpt(ah.Id, 70, basket("05-10-2026 10:00", 6000, groceriesA...))
	b := e.rcpt(ah.Id, 71, basket("05-10-2026 10:00", 6000, groceriesA...))
	e.do("POST", fmt.Sprintf("/v1/receipts/%d/duplicate/keep", b), nil)
	c := e.rcpt(ah.Id, 72, basket("05-10-2026 10:00", 6000, groceriesA...))
	wantDup(t, e, c, a, "total+date+lines")
	wantNoDup(t, e, b, "kept")
}

// Removing the newer receipt (what "Same receipt, remove this one" does) reverts what it did to the list
// and to nothing else.
func TestDuplicateRemovalRevertsOnlyItsOwnChecks(t *testing.T) {
	t.Run("newer confirmed alone: its checks are undone", func(t *testing.T) {
		e := newEnv(t)
		ah := e.shop("Albert Heijn")
		e.addItem(map[string]any{"text": "yoghurt"})
		e.addItem(map[string]any{"text": "pepper"})
		e.rcpt(ah.Id, 80, []string{today() + " 10:00", "YOGHURT VOL|1,50", "TOTAAL|1,50"}) // older, unconfirmed
		rid, _ := e.confirmRows(ah.Id, 81, today()+" 10:00|", "YOGHURT VOL|1,50", "TOTAAL|1,50")
		if !isChecked(e.listItems()["yoghurt"]) {
			t.Fatal("setup: not checked")
		}
		if rec := e.do("DELETE", fmt.Sprintf("/v1/receipts/%d", rid), nil); rec.Code != 204 {
			t.Fatal(rec.Code)
		}
		it := e.listItems()["yoghurt"]
		if isChecked(it) || it["checked_receipt_id"] != nil {
			t.Errorf("yoghurt after removal: %v", it)
		}
		if isChecked(e.listItems()["pepper"]) {
			t.Error("unrelated item changed")
		}
		if n := count(t, e, `SELECT COUNT(*) FROM shopping_list_receipt_checks`); n != 0 {
			t.Errorf("%d check rows left", n)
		}
	})
	t.Run("both confirmed: the older receipt keeps its check, products and history", func(t *testing.T) {
		e := newEnv(t)
		ah := e.shop("Albert Heijn")
		e.addItem(map[string]any{"text": "yoghurt"})
		rows := []string{today() + " 10:00|", "YOGHURT VOL|1,50", "ARLA MELK|1,29", "TOTAAL|2,79"}
		older, _ := e.confirmRows(ah.Id, 82, rows...)
		newer, _ := e.confirmRows(ah.Id, 83, rows...)
		if e.dupOf(newer) == nil {
			t.Fatal("setup: newer receipt not flagged (3 items needed? total is under 10 euro, 2 shared lines)")
		}
		prods := count(t, e, `SELECT COUNT(*) FROM products`)
		linked := func() int {
			return count(t, e, `SELECT COUNT(*) FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id WHERE l.product_id IS NOT NULL AND r.status = 'ok'`)
		}
		before := linked()
		if rec := e.do("DELETE", fmt.Sprintf("/v1/receipts/%d", newer), nil); rec.Code != 204 {
			t.Fatal(rec.Code)
		}
		it := e.listItems()["yoghurt"]
		if !isChecked(it) || int64(it["checked_receipt_id"].(float64)) != older {
			t.Errorf("the older receipt's check was lost: %v", it)
		}
		if got := count(t, e, `SELECT COUNT(*) FROM products`); got != prods {
			t.Errorf("products %d -> %d: removing a receipt must not delete products", prods, got)
		}
		if got := linked(); got != before-2 {
			t.Errorf("purchases counted %d -> %d, want only the older receipt's 2 lines left", before, got)
		}
		if rec := e.do("GET", fmt.Sprintf("/v1/receipts/%d", older), nil); rec.Code != 200 {
			t.Errorf("older receipt: %d", rec.Code)
		}
		if rec := e.do("GET", fmt.Sprintf("/v1/receipts/%d/image", newer), nil); rec.Code != 404 {
			t.Errorf("deleted receipt still served: %d", rec.Code)
		}
	})
}
