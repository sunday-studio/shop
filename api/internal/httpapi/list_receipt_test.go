package httpapi_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Phase A: confirming a receipt checks off the shopping list.

func today() string        { return time.Now().Format("02-01-2006") }
func daysAgo(n int) string { return time.Now().AddDate(0, 0, -n).Format("02-01-2006") }

func (e *env) addItem(body map[string]any) map[string]any {
	e.t.Helper()
	rec := e.do("POST", "/v1/list", body)
	if rec.Code != 201 && rec.Code != 200 {
		e.t.Fatalf("add item: %d %s", rec.Code, rec.Body)
	}
	return jsonMap(e.t, rec.Body.String())
}

func (e *env) listItems() map[string]map[string]any {
	e.t.Helper()
	rec := e.do("GET", "/v1/list?include_checked=true", nil)
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		e.t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[r["text"].(string)] = r
	}
	return out
}

// confirmRows uploads a receipt with the given rows (first row is the date) and confirms it.
func (e *env) confirmRows(shopID int64, seed uint8, rows ...string) (int64, map[string]any) {
	e.t.Helper()
	e.ocr.rows.Store(rows)
	r := jsonMap(e.t, e.upload(shopID, pngBytes(seed), nil).Body.String())
	e.drain()
	id := int64(r["id"].(float64))
	rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", id), nil)
	if rec.Code != 200 {
		e.t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	return id, jsonMap(e.t, rec.Body.String())
}

func listOf(t *testing.T, confirm map[string]any, key string) []any {
	t.Helper()
	l, ok := confirm["list"].(map[string]any)
	if !ok {
		t.Fatalf("no list block in confirm response: %v", confirm["list"])
	}
	return l[key].([]any)
}

func isChecked(it map[string]any) bool { _, ok := it["checked_at"]; return ok }

func TestConfirmChecksPlannedItems(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Arla melk"})
	e.addItem(map[string]any{"product_id": milk.Id})
	e.addItem(map[string]any{"text": "yoghurt"})       // free text, close to a receipt line: checked
	e.addItem(map[string]any{"text": "havermout bio"}) // looser: suggestion only
	e.addItem(map[string]any{"text": "pepper"})        // nothing on the receipt

	rid, conf := e.confirmRows(shop.Id, 1, today()+" 10:00|", "ARLA MELK|1,29", "AH HAVERMOUT|2,10", "YOGHURT VOL|1,50", "TOTAAL|4,89")

	if got := len(listOf(t, conf, "checked")); got != 2 {
		t.Fatalf("checked = %d, want 2 (Arla melk + yoghurt): %v", got, conf["list"])
	}
	sugs := listOf(t, conf, "suggestions")
	if len(sugs) != 1 || sugs[0].(map[string]any)["item_text"] != "havermout bio" || sugs[0].(map[string]any)["reason"] != "match" {
		t.Fatalf("suggestions = %v", sugs)
	}
	items := e.listItems()
	if !isChecked(items["Arla melk"]) || items["Arla melk"]["check_source"] != "receipt" ||
		int64(items["Arla melk"]["checked_receipt_id"].(float64)) != rid || items["Arla melk"]["checked_line_id"] == nil {
		t.Errorf("Arla melk = %v", items["Arla melk"])
	}
	if !isChecked(items["yoghurt"]) {
		t.Errorf("yoghurt not checked: %v", items["yoghurt"])
	}
	if isChecked(items["havermout bio"]) || isChecked(items["pepper"]) {
		t.Errorf("suggestion/unmatched item got checked: %v %v", items["havermout bio"], items["pepper"])
	}

	// GET /v1/list/suggestions lists the open suggestion.
	var list []map[string]any
	_ = json.Unmarshal(e.do("GET", "/v1/list/suggestions", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0]["line_text"] != "AH HAVERMOUT" {
		t.Fatalf("GET suggestions = %v", list)
	}
	// The changes feed carries the checked items and the open suggestion.
	ch := jsonMap(t, e.do("GET", "/v1/list/changes?since="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), nil).Body.String())
	if len(ch["suggestions"].([]any)) != 1 {
		t.Errorf("changes suggestions = %v", ch["suggestions"])
	}
	nChecked := 0
	for _, it := range ch["items"].([]any) {
		if _, ok := it.(map[string]any)["checked_at"]; ok {
			nChecked++
		}
	}
	if nChecked != 2 {
		t.Errorf("changes feed checked items = %d", nChecked)
	}

	// Accept: the item is checked by the receipt; accepting again is harmless; dismissing it then conflicts.
	sid := int64(list[0]["id"].(float64))
	acc := jsonMap(t, e.do("POST", fmt.Sprintf("/v1/list/suggestions/%d/accept", sid), nil).Body.String())
	if acc["check_source"] != "receipt" || acc["text"] != "havermout bio" {
		t.Errorf("accept = %v", acc)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/list/suggestions/%d/accept", sid), nil); rec.Code != 200 {
		t.Errorf("second accept: %d", rec.Code)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/list/suggestions/%d/dismiss", sid), nil); rec.Code != 409 {
		t.Errorf("dismiss after accept: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/list/suggestions/9999/accept", nil); rec.Code != 404 {
		t.Errorf("unknown suggestion: %d", rec.Code)
	}

	// Undo puts everything back, including the accepted suggestion; a second undo restores nothing.
	und := jsonMap(t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", rid), nil).Body.String())
	if n := len(und["restored"].([]any)); n != 3 {
		t.Errorf("restored = %d, want 3", n)
	}
	for text, it := range e.listItems() {
		if isChecked(it) {
			t.Errorf("%s still checked after undo", text)
		}
	}
	und = jsonMap(t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", rid), nil).Body.String())
	if n := len(und["restored"].([]any)); n != 0 {
		t.Errorf("second undo restored %d", n)
	}
	// Re-confirming the same receipt must not re-check what the user undid.
	conf2 := jsonMap(t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", rid), nil).Body.String())
	if n := len(listOf(t, conf2, "checked")); n != 0 {
		t.Errorf("re-confirm after undo checked %d", n)
	}
	if rec := e.do("POST", "/v1/receipts/9999/list-checks/undo", nil); rec.Code != 404 {
		t.Errorf("undo unknown receipt: %d", rec.Code)
	}
}

func TestDismissedSuggestionStaysDismissed(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.addItem(map[string]any{"text": "havermout bio"})
	rid, conf := e.confirmRows(shop.Id, 2, today()+"|", "AH HAVERMOUT|2,10", "TOTAAL|2,10")
	sugs := listOf(t, conf, "suggestions")
	if len(sugs) != 1 {
		t.Fatalf("suggestions = %v", sugs)
	}
	sid := int64(sugs[0].(map[string]any)["id"].(float64))
	for i := 0; i < 2; i++ { // idempotent
		if rec := e.do("POST", fmt.Sprintf("/v1/list/suggestions/%d/dismiss", sid), nil); rec.Code != 204 {
			t.Fatalf("dismiss: %d", rec.Code)
		}
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/list/suggestions/%d/accept", sid), nil); rec.Code != 409 {
		t.Errorf("accept after dismiss: %d", rec.Code)
	}
	conf = jsonMap(t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", rid), nil).Body.String())
	if n := len(listOf(t, conf, "suggestions")); n != 0 {
		t.Errorf("re-confirm re-suggested a dismissed item (%d)", n)
	}
	var list []map[string]any
	_ = json.Unmarshal(e.do("GET", "/v1/list/suggestions", nil).Body.Bytes(), &list)
	if len(list) != 0 {
		t.Errorf("open suggestions = %v", list)
	}
}

func TestPartialQuantityKeepsItemOpen(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Arla melk"})
	e.addItem(map[string]any{"product_id": milk.Id, "qty": 3, "note": "voor de koffie"})

	rid, conf := e.confirmRows(shop.Id, 3, today()+"|", "ARLA MELK|1,29", "TOTAAL|1,29")
	if len(listOf(t, conf, "checked")) != 0 || len(listOf(t, conf, "reduced")) != 1 {
		t.Fatalf("list = %v", conf["list"])
	}
	it := e.listItems()["Arla melk"]
	if isChecked(it) || it["qty"].(float64) != 2 || it["note"] != "voor de koffie · 1 of 3 bought" {
		t.Fatalf("item = %v", it)
	}
	// Same receipt again: nothing more is taken off.
	e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", rid), nil)
	if it := e.listItems()["Arla melk"]; it["qty"].(float64) != 2 {
		t.Errorf("repeat confirm changed qty: %v", it)
	}
	// Undo restores qty and note exactly.
	e.do("POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", rid), nil)
	it = e.listItems()["Arla melk"]
	if it["qty"].(float64) != 3 || it["note"] != "voor de koffie" || isChecked(it) {
		t.Errorf("after undo = %v", it)
	}

	// A receipt with more than needed checks the item fully.
	e.confirmRows(shop.Id, 4, today()+"|", "ARLA MELK|1,29", "ARLA MELK|1,29", "ARLA MELK|1,29", "ARLA MELK|1,29", "TOTAAL|5,16")
	if !isChecked(e.listItems()["Arla melk"]) {
		t.Errorf("not checked by a bigger receipt: %v", e.listItems()["Arla melk"])
	}
}

func TestOldReceiptNeverTouchesList(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Arla melk"})
	e.addItem(map[string]any{"product_id": milk.Id})
	e.addItem(map[string]any{"text": "yoghurt"})

	// Two weeks old: nothing, and no block of suggestions either.
	_, conf := e.confirmRows(shop.Id, 5, daysAgo(14)+"|", "ARLA MELK|1,29", "YOGHURT VOL|1,50", "TOTAAL|2,79")
	if n := len(listOf(t, conf, "checked")) + len(listOf(t, conf, "suggestions")) + len(listOf(t, conf, "reduced")); n != 0 {
		t.Errorf("old receipt changed the list: %v", conf["list"])
	}
	for text, it := range e.listItems() {
		if isChecked(it) {
			t.Errorf("%s checked by an old receipt", text)
		}
	}
	// Three days old still counts; four does not.
	_, conf = e.confirmRows(shop.Id, 6, daysAgo(4)+"|", "ARLA MELK|1,29", "TOTAAL|1,29")
	if len(listOf(t, conf, "checked")) != 0 {
		t.Errorf("4-day-old receipt checked: %v", conf["list"])
	}
	_, conf = e.confirmRows(shop.Id, 7, daysAgo(3)+"|", "ARLA MELK|1,29", "TOTAAL|1,29")
	if len(listOf(t, conf, "checked")) != 1 {
		t.Errorf("3-day-old receipt did not check: %v", conf["list"])
	}
}

func TestUnconfirmedAndNoDateReceiptsAreIgnored(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Arla melk"})
	e.addItem(map[string]any{"product_id": milk.Id})

	// An uploaded but unconfirmed receipt does nothing.
	e.receipt(shop.Id, 8, today()+"|", "ARLA MELK|1,29", "TOTAAL|1,29")
	if isChecked(e.listItems()["Arla melk"]) {
		t.Fatal("unconfirmed receipt checked an item")
	}
	// A confirmed receipt with no readable date cannot be shown to be recent.
	_, conf := e.confirmRows(shop.Id, 9, "ARLA MELK|1,29", "TOTAAL|1,29")
	if len(listOf(t, conf, "checked")) != 0 || isChecked(e.listItems()["Arla melk"]) {
		t.Errorf("receipt without a date checked an item: %v", conf["list"])
	}
}

func TestSubstituteIsOnlyASuggestion(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	var dairy int64
	rec := e.do("GET", "/v1/categories", nil)
	for _, c := range decodeAs[[]map[string]any](t, rec) {
		if c["name"] == "Dairy" {
			dairy = int64(c["id"].(float64))
		}
	}
	arla := e.product(map[string]any{"name": "Halfvolle melk Arla", "category_id": dairy})
	other := e.product(map[string]any{"name": "Halfvolle melk Campina", "category_id": dairy})
	e.addItem(map[string]any{"product_id": arla.Id})
	e.ocr.rows.Store([]string{today() + "|", "HALFVOLLE MELK CAMPINA|1,29", "TOTAAL|1,29"})
	r := jsonMap(t, e.upload(shop.Id, pngBytes(10), nil).Body.String())
	e.drain()
	rid := int64(r["id"].(float64))
	// Link the line to Campina explicitly, then confirm.
	rc := decodeAs[map[string]any](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", rid), nil))
	lineID := int64(rc["lines"].([]any)[0].(map[string]any)["id"].(float64))
	e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", lineID), map[string]any{"product_id": other.Id})
	conf := jsonMap(t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", rid), nil).Body.String())
	if len(listOf(t, conf, "checked")) != 0 {
		t.Fatalf("a substitute was checked: %v", conf["list"])
	}
	sugs := listOf(t, conf, "suggestions")
	if len(sugs) != 1 || sugs[0].(map[string]any)["reason"] != "substitute" {
		t.Fatalf("suggestions = %v", sugs)
	}
}

func TestDeletingOrReparsingReceiptRevertsItsChecks(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Arla melk"})
	e.addItem(map[string]any{"product_id": milk.Id, "qty": 2})
	e.addItem(map[string]any{"text": "yoghurt"})

	rid, _ := e.confirmRows(shop.Id, 11, today()+"|", "ARLA MELK|1,29", "YOGHURT VOL|1,50", "TOTAAL|2,79")
	if !isChecked(e.listItems()["yoghurt"]) || e.listItems()["Arla melk"]["qty"].(float64) != 1 {
		t.Fatalf("setup: %v", e.listItems())
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/receipts/%d", rid), nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	items := e.listItems()
	if isChecked(items["yoghurt"]) || items["Arla melk"]["qty"].(float64) != 2 {
		t.Errorf("deleting the receipt left its checks: %v", items)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM shopping_list_receipt_checks`); n != 0 {
		t.Errorf("%d check rows left", n)
	}

	// A forced re-parse unconfirms the receipt and reverts the same way, and a new confirm applies again.
	rid, _ = e.confirmRows(shop.Id, 12, today()+"|", "ARLA MELK|1,29", "YOGHURT VOL|1,50", "TOTAAL|2,79")
	if !isChecked(e.listItems()["yoghurt"]) {
		t.Fatal("setup 2")
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/reparse?force=true", rid), nil); rec.Code != 202 {
		t.Fatalf("reparse: %d %s", rec.Code, rec.Body)
	}
	if isChecked(e.listItems()["yoghurt"]) {
		t.Errorf("forced re-parse left the check")
	}
}

func TestUserEditsSurviveUndo(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.addItem(map[string]any{"text": "yoghurt"})
	rid, _ := e.confirmRows(shop.Id, 13, today()+"|", "YOGHURT VOL|1,50", "TOTAAL|1,50")
	it := e.listItems()["yoghurt"]
	id := int64(it["id"].(float64))
	// The user unchecks and re-checks it by hand: the receipt no longer owns the check.
	e.do("POST", fmt.Sprintf("/v1/list/%d/uncheck", id), nil)
	again := jsonMap(t, e.do("POST", fmt.Sprintf("/v1/list/%d/check", id), nil).Body.String())
	if again["check_source"] != "manual" || again["checked_receipt_id"] != nil {
		t.Errorf("manual check = %v", again)
	}
	und := jsonMap(t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", rid), nil).Body.String())
	if len(und["restored"].([]any)) != 0 || !isChecked(e.listItems()["yoghurt"]) {
		t.Errorf("undo touched a manual check: %v", und)
	}
}

func TestListStepFailureNeverFailsConfirm(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.addItem(map[string]any{"text": "yoghurt"})
	if _, err := e.conn.Exec(`DROP TABLE shopping_list_receipt_checks`); err != nil {
		t.Fatal(err)
	}
	_, conf := e.confirmRows(shop.Id, 14, today()+"|", "YOGHURT VOL|1,50", "TOTAAL|1,50")
	if conf["status"] != "ok" || conf["list"] != nil {
		t.Errorf("confirm with a broken list step: status=%v list=%v", conf["status"], conf["list"])
	}
}
