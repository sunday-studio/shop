package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"
)

// Phase B: groups and checklists inside the shopping list.

func (e *env) mustDo(method, path string, body any, want ...int) map[string]any {
	e.t.Helper()
	rec := e.do(method, path, body)
	ok := false
	for _, w := range want {
		ok = ok || rec.Code == w
	}
	if !ok {
		e.t.Fatalf("%s %s: %d %s (want %v)", method, path, rec.Code, rec.Body, want)
	}
	if rec.Body.Len() == 0 {
		return nil
	}
	return jsonMap(e.t, rec.Body.String())
}

func lineItems(texts ...string) []map[string]any {
	out := make([]map[string]any, len(texts))
	for i, t := range texts {
		out[i] = map[string]any{"text": t}
	}
	return out
}

func idOf(m map[string]any) int64 { return int64(m["id"].(float64)) }

func ids(v any) []int64 {
	out := []int64{}
	for _, x := range v.([]any) {
		out = append(out, int64(x.(float64)))
	}
	return out
}

func (e *env) groupItems(g map[string]any) []map[string]any {
	var out []map[string]any
	for _, it := range g["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func TestGroupCRUDShapes(t *testing.T) {
	e := newEnv(t)
	g := e.mustDo("POST", "/v1/list/groups", map[string]any{"client_id": "g-1", "name": "BBQ", "note": "zaterdag", "items": lineItems("kip", "bier", "brood")}, 201)
	if g["kind"] != "checklist" || g["source"] != "user" || g["name"] != "BBQ" || g["note"] != "zaterdag" ||
		g["item_count"].(float64) != 3 || g["checked_count"].(float64) != 0 || len(g["item_ids"].([]any)) != 3 ||
		g["rev"] == nil || g["created_at"] == nil || g["sort"] == nil {
		t.Fatalf("created group = %v", g)
	}
	gid := idOf(g)
	for i, it := range e.groupItems(g) {
		if int64(it["group_id"].(float64)) != gid || idOf(it) != ids(g["item_ids"])[i] || it["rev"].(float64) == 0 {
			t.Errorf("item %d = %v", i, it)
		}
	}

	// Idempotency-Key works as the client id.
	req := func() *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"name": "Soep", "items": lineItems("ui")})
		r := httptest.NewRequest("POST", "/v1/list/groups", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+e.token)
		r.Header.Set("Idempotency-Key", "key-1")
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w
	}
	if a, b := req(), req(); a.Code != 201 || b.Code != 200 || idOf(jsonMap(t, a.Body.String())) != idOf(jsonMap(t, b.Body.String())) {
		t.Fatalf("Idempotency-Key: %d then %d", a.Code, b.Code)
	}

	// GET list, one, patch, archive.
	list := e.do("GET", "/v1/list/groups", nil)
	if list.Header().Get("X-List-Rev") == "" {
		t.Errorf("X-List-Rev header missing")
	}
	var gs []map[string]any
	_ = json.Unmarshal(list.Body.Bytes(), &gs)
	if len(gs) != 2 || gs[0]["name"] != "BBQ" || gs[0]["items"] != nil {
		t.Errorf("groups = %v", gs)
	}
	one := e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
	if len(e.groupItems(one)) != 3 {
		t.Errorf("get one: %v", one)
	}
	upd := e.mustDo("PATCH", fmt.Sprintf("/v1/list/groups/%d", gid), map[string]any{"name": "BBQ zaterdag", "archived": true}, 200)
	if upd["name"] != "BBQ zaterdag" || upd["archived_at"] == nil {
		t.Errorf("patch = %v", upd)
	}
	_ = json.Unmarshal(e.do("GET", "/v1/list/groups", nil).Body.Bytes(), &gs)
	if len(gs) != 1 {
		t.Errorf("archived should be hidden: %d", len(gs))
	}
	_ = json.Unmarshal(e.do("GET", "/v1/list/groups?include_archived=true&include_items=true", nil).Body.Bytes(), &gs)
	if len(gs) != 2 || gs[1]["archived_at"] == nil || len(gs[1]["items"].([]any)) != 3 {
		t.Errorf("with archived: %v", gs)
	}

	// Errors.
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"POST", "/v1/list/groups", map[string]any{"name": ""}, 400},
		{"POST", "/v1/list/groups", map[string]any{"name": "x", "kind": "weird"}, 400},
		{"POST", "/v1/list/groups", map[string]any{"name": "x", "bogus": 1}, 400},
		{"POST", "/v1/list/groups", map[string]any{"name": "x", "items": []map[string]any{{"qty": 1}}}, 400},
		{"GET", "/v1/list/groups/9999", nil, 404},
		{"PATCH", "/v1/list/groups/9999", map[string]any{"name": "x"}, 404},
		{"PATCH", fmt.Sprintf("/v1/list/groups/%d", gid), map[string]any{"name": " "}, 400},
		{"DELETE", "/v1/list/groups/9999", nil, 404},
		{"DELETE", fmt.Sprintf("/v1/list/groups/%d?items=maybe", gid), nil, 400},
		{"POST", "/v1/list/groups/9999/items", map[string]any{"items": lineItems("x")}, 404},
		{"POST", fmt.Sprintf("/v1/list/groups/%d/items", gid), map[string]any{"items": []any{}}, 400},
		{"POST", "/v1/list", map[string]any{"text": "x", "group_id": 9999}, 404},
		{"POST", "/v1/list/groups/9999/restore", nil, 404},
		{"POST", fmt.Sprintf("/v1/list/groups/%d/instantiate", gid), nil, 400},
		{"GET", "/v1/list/groups?kind=nope", nil, 400},
	} {
		if rec := e.do(c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s %v: %d, want %d (%s)", c.method, c.path, c.body, rec.Code, c.want, rec.Body)
		}
	}
}

func TestGroupItemsAddMoveDelete(t *testing.T) {
	e := newEnv(t)
	milk := e.product(map[string]any{"name": "Arla melk"})
	g := e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "Ontbijt"}, 201)
	gid := idOf(g)
	if g["item_count"].(float64) != 0 || len(g["item_ids"].([]any)) != 0 {
		t.Errorf("empty group = %v", g)
	}

	// Bulk add; the same product twice is one item.
	added := e.do("POST", fmt.Sprintf("/v1/list/groups/%d/items", gid), map[string]any{"items": []map[string]any{
		{"product_id": milk.Id}, {"text": "jam"}, {"product_id": milk.Id, "qty": 2},
	}})
	var items []map[string]any
	_ = json.Unmarshal(added.Body.Bytes(), &items)
	if added.Code != 200 || len(items) != 3 || idOf(items[0]) != idOf(items[2]) || items[2]["qty"].(float64) != 3 {
		t.Fatalf("bulk add: %d %s", added.Code, added.Body)
	}

	// One item at a time with a client id, retried: still one item.
	body := map[string]any{"text": "boter", "group_id": gid, "client_id": "i-1"}
	first := e.mustDo("POST", "/v1/list", body, 201)
	second := e.mustDo("POST", "/v1/list", body, 200)
	if idOf(first) != idOf(second) || int64(first["group_id"].(float64)) != gid {
		t.Errorf("item retry: %v %v", first, second)
	}
	if n := len(e.groupItems(e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200))); n != 3 {
		t.Errorf("group has %d items, want 3", n)
	}

	// Milk is also on the main list: both rows know about the other.
	main := e.addItem(map[string]any{"product_id": milk.Id})
	g1 := e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
	row := e.groupItems(g1)[0]
	if row["on_main_list"] != true || row["other_group_ids"] != nil {
		t.Errorf("group row = %v", row)
	}
	if idOf(main) == idOf(row) || int64(row["group_id"].(float64)) != gid {
		t.Errorf("main row = %v", main)
	}

	// Move: boter to the main list, then into a second group; null really means the main list.
	g2 := e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "Lunch"}, 201)
	mv := e.mustDo("POST", fmt.Sprintf("/v1/list/%d/move", idOf(first)), map[string]any{"group_id": nil}, 200)
	if mv["group_id"] != nil {
		t.Errorf("move to main: %v", mv)
	}
	mv = e.mustDo("POST", fmt.Sprintf("/v1/list/%d/move", idOf(first)), map[string]any{"group_id": idOf(g2)}, 200)
	if int64(mv["group_id"].(float64)) != idOf(g2) {
		t.Errorf("move to group: %v", mv)
	}
	for _, c := range []struct {
		body map[string]any
		want int
	}{{map[string]any{}, 400}, {map[string]any{"group_id": "x"}, 400}, {map[string]any{"group_id": 9999}, 404}} {
		if rec := e.do("POST", fmt.Sprintf("/v1/list/%d/move", idOf(first)), c.body); rec.Code != c.want {
			t.Errorf("move %v: %d want %d", c.body, rec.Code, c.want)
		}
	}
	if rec := e.do("POST", "/v1/list/9999/move", map[string]any{"group_id": nil}); rec.Code != 404 {
		t.Errorf("move unknown item: %d", rec.Code)
	}

	// Delete keeping the items: they land on the main list (milk merges nowhere, it stays its own row).
	del := e.mustDo("DELETE", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
	if len(del["kept"].([]any)) != 2 || len(del["deleted_item_ids"].([]any)) != 0 || del["restore_until"] == nil || int64(del["group_id"].(float64)) != gid {
		t.Errorf("delete keep = %v", del)
	}
	for _, it := range del["kept"].([]any) {
		if it.(map[string]any)["group_id"] != nil {
			t.Errorf("kept item still has a group: %v", it)
		}
	}
	// Undo.
	back := e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/restore", gid), nil, 200)
	if idOf(back) != gid || len(e.groupItems(back)) != 2 {
		t.Errorf("restore = %v", back)
	}
	// Delete everything, undo again.
	del = e.mustDo("DELETE", fmt.Sprintf("/v1/list/groups/%d?items=delete", gid), nil, 200)
	if len(del["kept"].([]any)) != 0 || len(del["deleted_item_ids"].([]any)) != 2 {
		t.Errorf("delete all = %v", del)
	}
	back = e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/restore", gid), nil, 200)
	if len(e.groupItems(back)) != 2 {
		t.Errorf("restore after delete = %v", back)
	}
}

func TestTemplatesOverHTTP(t *testing.T) {
	e := newEnv(t)
	g := e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "Jerk chicken", "items": []map[string]any{{"text": "kip", "qty": 6}, {"text": "thyme"}}}, 201)
	e.mustDo("POST", fmt.Sprintf("/v1/list/%d/check", ids(g["item_ids"])[0]), nil, 200)

	tpl := e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/save-as-template", idOf(g)), map[string]any{"client_id": "t-1"}, 201)
	if tpl["kind"] != "template" || tpl["item_count"].(float64) != 2 || tpl["checked_count"].(float64) != 0 {
		t.Fatalf("template = %v", tpl)
	}
	e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/save-as-template", idOf(g)), map[string]any{"client_id": "t-1"}, 200)

	var tpls []map[string]any
	_ = json.Unmarshal(e.do("GET", "/v1/list/groups?kind=template", nil).Body.Bytes(), &tpls)
	if len(tpls) != 1 || tpls[0]["item_count"].(float64) != 2 {
		t.Errorf("templates = %v", tpls)
	}

	// Template items are not shopping.
	flat := e.do("GET", "/v1/list?include_checked=true", nil)
	var flatItems []map[string]any
	_ = json.Unmarshal(flat.Body.Bytes(), &flatItems)
	if len(flatItems) != 2 {
		t.Errorf("GET /v1/list has %d items, want 2 (the checklist's)", len(flatItems))
	}

	inst := e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/instantiate", idOf(tpl)), map[string]any{"name": "Jerk chicken zaterdag", "client_id": "i-1"}, 201)
	if inst["kind"] != "checklist" || inst["name"] != "Jerk chicken zaterdag" || len(inst["item_ids"].([]any)) != 2 || inst["checked_count"].(float64) != 0 {
		t.Fatalf("instance = %v", inst)
	}
	again := e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/instantiate", idOf(tpl)), map[string]any{"name": "Jerk chicken zaterdag", "client_id": "i-1"}, 200)
	if idOf(again) != idOf(inst) {
		t.Errorf("instantiate retry made another group")
	}
	// No body at all is fine too.
	e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/instantiate", idOf(tpl)), nil, 201)

	// Deleting a template does not put its items on the list.
	del := e.mustDo("DELETE", fmt.Sprintf("/v1/list/groups/%d", idOf(tpl)), nil, 200)
	if len(del["kept"].([]any)) != 0 {
		t.Errorf("template delete kept items: %v", del)
	}
}

// A purchase checks the product everywhere it is open, but not in templates or archived groups.
func TestReceiptChecksGroupedItems(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	milk := e.product(map[string]any{"name": "Arla melk"})
	bbq := idOf(e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "BBQ"}, 201))
	old := idOf(e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "Oud"}, 201))
	tpl := idOf(e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "Sjabloon", "kind": "template"}, 201))
	soup := idOf(e.mustDo("POST", "/v1/list/groups", map[string]any{"name": "Soep"}, 201))

	add := func(gid int64, body map[string]any) map[string]any {
		if gid != 0 {
			body["group_id"] = gid
		}
		return e.addItem(body)
	}
	mainMilk := add(0, map[string]any{"product_id": milk.Id})
	bbqMilk := add(bbq, map[string]any{"product_id": milk.Id})
	oldMilk := add(old, map[string]any{"product_id": milk.Id})
	tplMilk := add(tpl, map[string]any{"product_id": milk.Id})
	mainYog := add(0, map[string]any{"text": "yoghurt"})
	soupYog := add(soup, map[string]any{"text": "yoghurt"})
	soupPepper := add(soup, map[string]any{"text": "pepper"})
	e.mustDo("PATCH", fmt.Sprintf("/v1/list/groups/%d", old), map[string]any{"archived": true}, 200)

	rid, conf := e.confirmRows(shop.Id, 1, today()+" 10:00|", "ARLA MELK|1,29", "YOGHURT VOL|1,50", "TOTAAL|2,79")
	if n := len(listOf(t, conf, "checked")); n != 4 {
		t.Fatalf("checked %d items, want 4 (milk on the main list and in BBQ, yoghurt on the main list and in Soep): %v", n, conf["list"])
	}
	byID := map[int64]map[string]any{}
	for _, it := range e.mustDo("GET", "/v1/list/changes?since_rev=0", nil, 200)["items"].([]any) {
		m := it.(map[string]any)
		byID[idOf(m)] = m
	}
	for name, it := range map[string]map[string]any{"main milk": mainMilk, "bbq milk": bbqMilk, "main yoghurt": mainYog, "soup yoghurt": soupYog} {
		got := byID[idOf(it)]
		if !isChecked(got) || got["check_source"] != "receipt" || int64(got["checked_receipt_id"].(float64)) != rid {
			t.Errorf("%s not checked by the receipt: %v", name, got)
		}
	}
	for name, it := range map[string]map[string]any{"archived milk": oldMilk, "pepper": soupPepper} {
		if isChecked(byID[idOf(it)]) {
			t.Errorf("%s was checked", name)
		}
	}
	if _, ok := byID[idOf(tplMilk)]; ok {
		t.Errorf("template item is in the feed")
	}
	if got := e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", tpl), nil, 200); isChecked(e.groupItems(got)[0]) {
		t.Errorf("template item was checked")
	}
	soupG := e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", soup), nil, 200)
	if soupG["checked_count"].(float64) != 1 || soupG["item_count"].(float64) != 2 {
		t.Errorf("soup progress = %v", soupG)
	}

	// Undo puts every grouped row back.
	und := e.mustDo("POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", rid), nil, 200)
	if n := len(und["restored"].([]any)); n != 4 {
		t.Errorf("restored %d, want 4", n)
	}
	for _, it := range e.mustDo("GET", "/v1/list/changes?since_rev=0", nil, 200)["items"].([]any) {
		if isChecked(it.(map[string]any)) {
			t.Errorf("still checked after undo: %v", it)
		}
	}
}

// phone is a tiny offline client: it applies the changes feed the way the app must.
type phone struct {
	items  map[int64]map[string]any
	groups map[int64]map[string]any
	rev    int64
}

func newPhone() *phone {
	return &phone{items: map[int64]map[string]any{}, groups: map[int64]map[string]any{}}
}

func (p *phone) sync(e *env) map[string]any {
	e.t.Helper()
	ch := e.mustDo("GET", "/v1/list/changes?since_rev="+strconv.FormatInt(p.rev, 10), nil, 200)
	if ch["full_resync"] == true {
		p.items, p.groups = map[int64]map[string]any{}, map[int64]map[string]any{}
	}
	for _, it := range ch["items"].([]any) {
		m := it.(map[string]any)
		p.items[idOf(m)] = m
	}
	for _, g := range ch["groups"].([]any) {
		m := g.(map[string]any)
		p.groups[idOf(m)] = m
	}
	for _, id := range ids(ch["deleted_ids"]) {
		delete(p.items, id)
	}
	for _, id := range ids(ch["deleted_group_ids"]) {
		delete(p.groups, id)
	}
	p.rev = int64(ch["rev"].(float64))
	return ch
}

// shape reduces a phone's copy to what must agree with the server: item text, group and checked state.
func shape(items map[int64]map[string]any, groups map[int64]map[string]any) map[string]any {
	is := []string{}
	for id, it := range items {
		g := int64(0)
		if v, ok := it["group_id"]; ok {
			g = int64(v.(float64))
		}
		is = append(is, fmt.Sprintf("%d:%v:g%d:c%v", id, it["text"], g, isChecked(it)))
	}
	gs := []string{}
	for id, g := range groups {
		_, arch := g["archived_at"]
		gs = append(gs, fmt.Sprintf("%d:%v:%v:a%v", id, g["name"], g["kind"], arch))
	}
	sort.Strings(is)
	sort.Strings(gs)
	return map[string]any{"items": is, "groups": gs}
}

func (e *env) serverShape() map[string]any {
	e.t.Helper()
	items := map[int64]map[string]any{}
	rec := e.do("GET", "/v1/list?include_checked=true", nil)
	var rows []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	for _, r := range rows {
		items[idOf(r)] = r
	}
	groups := map[int64]map[string]any{}
	var gs []map[string]any
	_ = json.Unmarshal(e.do("GET", "/v1/list/groups?include_archived=true", nil).Body.Bytes(), &gs)
	for _, g := range gs {
		groups[idOf(g)] = g
	}
	return shape(items, groups)
}

func TestSyncRoundTripTwoPhones(t *testing.T) {
	e := newEnv(t)
	a, b := newPhone(), newPhone()

	// Phone A, offline, makes a group with 12 items and adds 2 more; the connection drops on every call,
	// so each request is sent twice.
	lines := make([]map[string]any, 12)
	for i := range lines {
		lines[i] = map[string]any{"text": fmt.Sprintf("item %02d", i+1)}
	}
	create := map[string]any{"client_id": "a-group-1", "name": "Feest", "items": lines}
	g := e.mustDo("POST", "/v1/list/groups", create, 201)
	for i := 0; i < 2; i++ {
		again := e.mustDo("POST", "/v1/list/groups", create, 200)
		if idOf(again) != idOf(g) || !reflect.DeepEqual(ids(again["item_ids"]), ids(g["item_ids"])) {
			t.Fatalf("retry %d changed the answer: %v", i, again)
		}
	}
	gid := idOf(g)
	for i := 13; i <= 14; i++ {
		body := map[string]any{"client_id": fmt.Sprintf("a-item-%d", i), "text": fmt.Sprintf("item %02d", i), "group_id": gid}
		e.mustDo("POST", "/v1/list", body, 201)
		e.mustDo("POST", "/v1/list", body, 200)
	}
	for _, id := range ids(g["item_ids"])[:5] {
		e.mustDo("POST", fmt.Sprintf("/v1/list/%d/check", id), nil, 200)
	}
	// "The server has exactly one group and 14 items."
	srv := e.serverShape()
	if len(srv["groups"].([]string)) != 1 || len(srv["items"].([]string)) != 14 {
		t.Fatalf("server: %v", srv)
	}

	// Phone B joins late and sees the same; so does A.
	b.sync(e)
	a.sync(e)
	if !reflect.DeepEqual(b.groups[gid]["name"], "Feest") || !reflect.DeepEqual(shape(b.items, b.groups), srv) || !reflect.DeepEqual(shape(a.items, a.groups), srv) {
		t.Fatalf("phones differ from the server:\nB %v\nserver %v", shape(b.items, b.groups), srv)
	}

	// Nothing changed: an idle sync returns nothing, with no repeated boundary item.
	idle := b.sync(e)
	if len(idle["items"].([]any))+len(idle["groups"].([]any))+len(idle["deleted_ids"].([]any))+len(idle["deleted_group_ids"].([]any)) != 0 || idle["full_resync"] != false {
		t.Errorf("idle sync = %v", idle)
	}

	// B edits (rename, check, add, move), A deletes the group keeping items; both converge.
	e.mustDo("PATCH", fmt.Sprintf("/v1/list/groups/%d", gid), map[string]any{"name": "Feest!"}, 200)
	a.sync(e)
	b.sync(e)
	if b.groups[gid]["name"] != "Feest!" || a.groups[gid]["name"] != "Feest!" {
		t.Errorf("rename did not reach both phones")
	}
	e.mustDo("DELETE", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
	b.sync(e)
	a.sync(e)
	srv = e.serverShape()
	if len(b.groups) != 0 || !reflect.DeepEqual(shape(b.items, b.groups), srv) || !reflect.DeepEqual(shape(a.items, a.groups), srv) {
		t.Fatalf("after delete: B %v / server %v", shape(b.items, b.groups), srv)
	}
	for _, it := range b.items {
		if it["group_id"] != nil {
			t.Fatalf("kept item still in a group on B: %v", it)
		}
	}

	// Undo: the group is back on both phones with its items.
	e.mustDo("POST", fmt.Sprintf("/v1/list/groups/%d/restore", gid), nil, 200)
	b.sync(e)
	a.sync(e)
	srv = e.serverShape()
	if len(b.groups) != 1 || !reflect.DeepEqual(shape(b.items, b.groups), srv) || !reflect.DeepEqual(shape(a.items, a.groups), srv) {
		t.Fatalf("after restore: B %v / server %v", shape(b.items, b.groups), srv)
	}

	// Delete with items; A is offline for good, B syncs. A comes back with its old cursor: still correct.
	e.mustDo("DELETE", fmt.Sprintf("/v1/list/groups/%d?items=delete", gid), nil, 200)
	b.sync(e)
	if len(b.items) != 0 || len(b.groups) != 0 {
		t.Errorf("B after delete all: %v", shape(b.items, b.groups))
	}
	a.sync(e)
	if !reflect.DeepEqual(shape(a.items, a.groups), e.serverShape()) {
		t.Errorf("A after delete all: %v", shape(a.items, a.groups))
	}

	// The time cursor keeps working for older apps and also reports the new fields.
	old := e.mustDo("GET", "/v1/list/changes?since="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), nil, 200)
	for _, k := range []string{"items", "deleted_ids", "server_time", "full_resync", "groups", "deleted_group_ids", "rev", "suggestions"} {
		if _, ok := old[k]; !ok {
			t.Errorf("time cursor response lacks %q", k)
		}
	}
	if len(old["deleted_group_ids"].([]any)) != 1 {
		t.Errorf("time cursor group deletions = %v", old["deleted_group_ids"])
	}
	for _, c := range []struct {
		path string
		want int
	}{
		{"/v1/list/changes", 400},
		{"/v1/list/changes?since_rev=abc", 400},
		{"/v1/list/changes?since_rev=-1", 400},
		{"/v1/list/changes?since_rev=99999", 400},
		{"/v1/list/changes?since=yesterday", 400},
	} {
		if rec := e.do("GET", c.path, nil); rec.Code != c.want {
			t.Errorf("GET %s: %d, want %d", c.path, rec.Code, c.want)
		}
	}
	if rec := e.do("GET", "/v1/list", nil); rec.Header().Get("X-List-Rev") != strconv.FormatInt(b.rev, 10) {
		t.Errorf("X-List-Rev = %q, want %d", rec.Header().Get("X-List-Rev"), b.rev)
	}
}

func TestGroupRoutesNeedNoSlashSurprises(t *testing.T) {
	// "groups" and "changes" are words, not ids: they must not be read as /v1/list/{id}.
	e := newEnv(t)
	if rec := e.do("PATCH", "/v1/list/groups", map[string]any{"text": "x"}); rec.Code == http.StatusOK {
		t.Errorf("PATCH /v1/list/groups answered %d", rec.Code)
	}
}

func TestMCPGroupTools(t *testing.T) {
	e := newEnv(t)
	milk := e.product(map[string]any{"name": "Arla melk"})
	cs, err := connectMCP(t, e, e.token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	type item struct {
		ID      int64  `json:"id"`
		Text    string `json:"text"`
		GroupID int64  `json:"group_id"`
		Checked bool   `json:"checked"`
	}
	type group struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Source       string `json:"source"`
		ItemCount    int64  `json:"item_count"`
		CheckedCount int64  `json:"checked_count"`
		Items        []item `json:"items"`
	}
	type created struct {
		Group   group `json:"group"`
		Created bool  `json:"created"`
	}
	args := map[string]any{"name": "Jerk chicken", "external_ref": "jerk-1", "items": []map[string]any{
		{"text": "kippendijen", "qty": 6}, {"text": "thyme"}, {"text": "Arla melk", "product": "Arla melk"},
	}}
	c1, res := call[created](t, cs, "shopping_group_create", args)
	if res.IsError || !c1.Created || c1.Group.Source != "assistant" || c1.Group.ItemCount != 3 || len(c1.Group.Items) != 3 {
		t.Fatalf("create: %+v %v", c1, errText(res))
	}
	// The assistant calls it again (a retry, or a second run): same group, nothing added.
	c2, _ := call[created](t, cs, "shopping_group_create", args)
	if c2.Created || c2.Group.ID != c1.Group.ID || c2.Group.ItemCount != 3 {
		t.Errorf("repeat: %+v", c2)
	}

	// The app sees it as a normal group from the assistant, and the group list tool agrees.
	api := e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", c1.Group.ID), nil, 200)
	if api["source"] != "assistant" || api["external_ref"] != "jerk-1" {
		t.Errorf("api group = %v", api)
	}
	for _, it := range e.groupItems(api) {
		if it["source"] != "assistant" {
			t.Errorf("item source = %v", it["source"])
		}
	}
	list, _ := call[struct {
		Groups []group `json:"groups"`
	}](t, cs, "shopping_group_list", map[string]any{"include_items": true})
	if len(list.Groups) != 1 || list.Groups[0].ID != c1.Group.ID || len(list.Groups[0].Items) != 3 {
		t.Errorf("group list = %+v", list)
	}

	// Add more items; the milk merges into the existing row.
	added, res := call[struct {
		Items []item `json:"items"`
	}](t, cs, "shopping_group_add_items", map[string]any{"group_id": c1.Group.ID, "items": []map[string]any{{"text": "rijst"}, {"text": "Arla melk", "product": "Arla melk", "qty": 2}}})
	if res.IsError || len(added.Items) != 2 {
		t.Fatalf("add items: %+v %v", added, errText(res))
	}
	if _, res := call[struct{}](t, cs, "shopping_group_add_items", map[string]any{"group_id": 9999, "items": []map[string]any{{"text": "x"}}}); !res.IsError {
		t.Errorf("add to an unknown group should be an error")
	}
	if g := e.mustDo("GET", fmt.Sprintf("/v1/list/groups/%d", c1.Group.ID), nil, 200); g["item_count"].(float64) != 4 {
		t.Errorf("group has %v items, want 4", g["item_count"])
	}

	// shopping_list_get shows group ids; shopping_list_add can target a group.
	one, _ := call[struct {
		Item item `json:"item"`
	}](t, cs, "shopping_list_add", map[string]any{"text": "mango", "group_id": c1.Group.ID})
	if one.Item.GroupID != c1.Group.ID {
		t.Errorf("list_add group = %+v", one)
	}
	all, _ := call[struct {
		Items []item `json:"items"`
	}](t, cs, "shopping_list_get", nil)
	if len(all.Items) != 5 || all.Items[0].GroupID != c1.Group.ID {
		t.Errorf("list_get = %+v", all)
	}
	_ = milk

	// Remove, keeping the items by default.
	rm, res := call[struct {
		OK        bool `json:"ok"`
		ItemsKept int  `json:"items_kept"`
	}](t, cs, "shopping_group_remove", map[string]any{"group_id": c1.Group.ID})
	if res.IsError || !rm.OK || rm.ItemsKept != 5 {
		t.Errorf("remove = %+v %v", rm, errText(res))
	}
	if _, res := call[struct{}](t, cs, "shopping_group_remove", map[string]any{"group_id": c1.Group.ID}); !res.IsError {
		t.Errorf("removing twice should be an error")
	}
	if _, res := call[struct{}](t, cs, "shopping_group_remove", map[string]any{"group_id": 1, "items": "some"}); !res.IsError {
		t.Errorf("bad items value should be an error")
	}
	if rec := e.do("GET", fmt.Sprintf("/v1/list/groups/%d", c1.Group.ID), nil); rec.Code != 404 {
		t.Errorf("group should be gone: %d", rec.Code)
	}
}
