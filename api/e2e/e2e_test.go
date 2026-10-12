//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// state carried from step to step (the loop is one story: pair, upload, read, confirm, ...).
var st struct {
	rid        int64 // synthetic receipt
	total      int
	lines      []M
	melkItem   int64
	bananaItem int64
	shopID     int64
	prodBanana int64
}

var syntheticLines = []recLine{
	{"AH HALFVOL MELK", 129},
	{"AH VOLKORENBROOD", 215},
	{"BANANEN", 189},
	{"GOUDA JONG BEL", 379},
	{"AH GRIEKSE YOGHURT", 119},
}

func needs(t *testing.T, ok bool, what string) {
	t.Helper()
	check(t, ok, "needs an earlier step: %s", what)
}

func TestE2E(t *testing.T) {
	step(t, "health + auth status before pairing", func(t *testing.T) {
		code, h := status(t, "GET", "/healthz", nil)
		check(t, code == 200 && str(h, "status") == "ok", "healthz %d %v", code, h)
		a := obj(t, "GET", "/v1/auth/status", nil, 200)
		check(t, str(a, "server") == "shop", "auth/status: %v", a)
		check(t, a["pairing_enabled"] == false, "pairing should be off before a password is set: %v", a)
		code, _, _, _ = raw("GET", "/v1/receipts", nil, false)
		check(t, code == 401, "unauthenticated /v1/receipts = %d, want 401", code)
	})

	step(t, "set password (shop CLI in container) + pair", func(t *testing.T) {
		out, err := compose(ctxBG(), pairPass+"\n", "exec", "-T", "api", "/usr/local/bin/shop", "password", "set")
		check(t, err == nil, "password set: %v: %s", err, out)
		out, err = compose(ctxBG(), "", "exec", "-T", "api", "/usr/local/bin/shop", "password", "status")
		check(t, err == nil && strings.Contains(out, "pairing password set: true"), "password status: %v: %s", err, out)
		a := obj(t, "GET", "/v1/auth/status", nil, 200)
		check(t, a["pairing_enabled"] == true, "pairing_enabled after set: %v", a)

		code, _, _, _ := raw("POST", "/v1/auth/pair", M{"password": "wrong", "device_name": "e2e"}, false)
		check(t, code == 401, "wrong password = %d, want 401", code)
		code, b, _, _ := raw("POST", "/v1/auth/pair", M{"password": pairPass, "device_name": "e2e iPhone"}, false)
		check(t, code == 201, "pair = %d: %s", code, trunc(b))
		m := decodeObj(t, b)
		token = str(m, "token")
		check(t, strings.HasPrefix(token, "st_"), "token shape: %q", token)
		me := obj(t, "GET", "/v1/auth/me", nil, 200)
		check(t, str(me, "kind") == "device" && str(me, "name") == "e2e iPhone", "me: %v", me)
		devs := arr(t, "GET", "/v1/auth/devices", nil, 200)
		check(t, len(devs) == 1, "devices: %v", devs)
	})

	step(t, "upload synthetic receipt, OCR + parse, dedupe, bad type", func(t *testing.T) {
		needs(t, token != "", "pairing")
		img, total := renderReceipt(syntheticLines, time.Now().In(amsterdam()))
		st.total = total
		code, r := upload(t, "e2e-receipt.jpg", img, "image/jpeg", nil)
		check(t, code == 202, "upload = %d %v", code, r)
		st.rid = num(r, "id")
		code, _ = upload(t, "e2e-receipt.jpg", img, "image/jpeg", nil)
		check(t, code == 409, "same image again = %d, want 409", code)
		code, _ = upload(t, "notes.txt", []byte("hello"), "text/plain", nil)
		check(t, code == 415, "text file = %d, want 415", code)
		heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
		code, _ = upload(t, "photo.heic", heic, "image/heic", nil)
		check(t, code == 415, "heic = %d, want 415", code)

		r = waitReceipt(t, st.rid, 10*time.Minute)
		note(t, "status %s, ocr %s %dms", str(r, "status"), str(r, "ocr_engine"), num(r, "ocr_ms"))
		check(t, str(r, "status") == "ok", "status %q, reasons %v, lines %v", str(r, "status"), r["review_reasons"], lineTexts(r))
	})

	step(t, "status, lines, date and totals (printed total = parsed sum)", func(t *testing.T) {
		needs(t, st.rid != 0, "upload")
		r := obj(t, "GET", fmt.Sprintf("/v1/receipts/%d", st.rid), nil, 200)
		items := itemLines(r)
		check(t, len(items) == len(syntheticLines), "item lines = %d, want %d: %v", len(items), len(syntheticLines), lineTexts(r))
		check(t, num(r, "total_cents") == int64(st.total), "total_cents = %d, want %d", num(r, "total_cents"), st.total)
		check(t, num(r, "parsed_sum_cents") == num(r, "total_cents"), "parsed sum %d != printed total %d", num(r, "parsed_sum_cents"), num(r, "total_cents"))
		check(t, sumLines(r) == int64(st.total), "sum of line totals %d != %d", sumLines(r), st.total)
		for i, want := range syntheticLines {
			l := items[i]
			check(t, num(l, "line_total_cents") == int64(want.cents), "line %d (%q) = %d, want %d", i, str(l, "raw_text"), num(l, "line_total_cents"), want.cents)
			check(t, strings.Contains(strings.ToUpper(str(l, "raw_text")), want.name), "line %d text %q, want %q", i, str(l, "raw_text"), want.name)
		}
		today := time.Now().In(amsterdam()).Format("2006-01-02")
		check(t, strings.HasPrefix(str(r, "purchased_at"), today) || str(r, "purchased_at") != "", "purchased_at %q", str(r, "purchased_at"))
		check(t, str(r, "purchased_at") != "" && dayOf(str(r, "purchased_at")) == today, "purchased_at %q, want local day %s", str(r, "purchased_at"), today)
		st.lines = items
		counts := obj(t, "GET", "/v1/receipts/counts", nil, 200)
		check(t, len(counts) > 0, "receipt counts empty")

		// reparse gives the same answer
		code, b, _, _ := raw("POST", fmt.Sprintf("/v1/receipts/%d/reparse", st.rid), nil, true)
		check(t, code == 200 || code == 202, "reparse = %d %s", code, trunc(b))
		r2 := waitReceipt(t, st.rid, 5*time.Minute)
		check(t, str(r2, "status") == "ok" && num(r2, "total_cents") == int64(st.total), "after reparse: %s total %d", str(r2, "status"), num(r2, "total_cents"))
	})

	step(t, "shop suggestion: read header, accept new shop", func(t *testing.T) {
		needs(t, st.rid != 0, "upload")
		s := obj(t, "GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", st.rid), nil, 200)
		sg, _ := s["suggestion"].(M)
		check(t, sg != nil, "no suggestion for an Albert Heijn header; lines: %v", lineTexts(obj(t, "GET", fmt.Sprintf("/v1/receipts/%d", st.rid), nil, 200)))
		check(t, str(sg, "kind") == "new" && strings.Contains(strings.ToLower(str(sg, "name")), "albert heijn"), "suggestion: %v", sg)
		res := obj(t, "POST", fmt.Sprintf("/v1/receipts/%d/shop-suggestion/accept", st.rid), M{}, 200)
		shop, _ := res["shop"].(M)
		st.shopID = num(shop, "id")
		check(t, st.shopID > 0, "accept: %v", res)
		r := obj(t, "GET", fmt.Sprintf("/v1/receipts/%d", st.rid), nil, 200)
		check(t, num(r, "shop_id") == st.shopID, "receipt shop_id %d, want %d", num(r, "shop_id"), st.shopID)
		s = obj(t, "GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", st.rid), nil, 200)
		check(t, s["suggestion"] == nil, "suggestion after accept: %v", s["suggestion"])
		shops := arr(t, "GET", "/v1/shops", nil, 200)
		check(t, len(shops) >= 1, "shops: %v", shops)
		note(t, "created shop %q", str(shop, "name"))
	})

	step(t, "shopping list: add items (idempotent client_id)", func(t *testing.T) {
		needs(t, len(st.lines) > 0, "lines")
		melk := obj(t, "POST", "/v1/list", M{"client_id": "e2e-melk", "text": str(st.lines[0], "raw_text")}, 201)
		st.melkItem = num(melk, "id")
		again := obj(t, "POST", "/v1/list", M{"client_id": "e2e-melk", "text": str(st.lines[0], "raw_text")}, 200)
		check(t, num(again, "id") == st.melkItem, "client_id repeat made a new item")
		ban := obj(t, "POST", "/v1/list", M{"client_id": "e2e-banaan", "text": str(st.lines[2], "raw_text"), "qty": 1}, 201)
		st.bananaItem = num(ban, "id")
		obj(t, "POST", "/v1/list", M{"client_id": "e2e-kaviaar", "text": "E2E Kaviaar"}, 201)
		open := arr(t, "GET", "/v1/list", nil, 200)
		check(t, len(open) == 3, "open items = %d, want 3", len(open))
	})

	step(t, "confirm receipt: auto products + list auto-check", func(t *testing.T) {
		needs(t, st.melkItem != 0, "list items")
		r := obj(t, "POST", fmt.Sprintf("/v1/receipts/%d/confirm", st.rid), nil, 200)
		check(t, str(r, "confirmed_at") != "", "no confirmed_at: %v", r)
		ap, _ := r["auto_products"].(M)
		check(t, ap != nil && num(ap, "linked")+num(ap, "created") == int64(len(syntheticLines)), "auto_products: %v", ap)
		check(t, ap["error"] == nil, "auto_products error: %v", ap["error"])
		lst, _ := r["list"].(M)
		check(t, lst != nil, "confirm response has no list part")
		checked := list(lst, "checked")
		check(t, len(checked) == 2, "auto-checked %d items, want 2 (melk + bananen): %v", len(checked), lst)
		full := obj(t, "GET", fmt.Sprintf("/v1/receipts/%d", st.rid), nil, 200)
		check(t, num(full, "unlinked_count") == 0, "unlinked item lines after confirm: %d", num(full, "unlinked_count"))
		for _, l := range itemLines(full) {
			if strings.Contains(str(l, "raw_text"), "BANANEN") {
				st.prodBanana = num(l, "product_id")
			}
		}
		check(t, st.prodBanana != 0, "banana line has no product")

		items := arr(t, "GET", "/v1/list?include_checked=true", nil, 200)
		got := map[int64]M{}
		for _, it := range items {
			got[num(it, "id")] = it
		}
		for _, id := range []int64{st.melkItem, st.bananaItem} {
			it := got[id]
			check(t, str(it, "checked_at") != "" && str(it, "check_source") == "receipt" && num(it, "checked_receipt_id") == st.rid, "item %d not auto-checked by the receipt: %v", id, it)
		}
		var open int
		for _, it := range items {
			if str(it, "checked_at") == "" {
				open++
				check(t, str(it, "text") == "E2E Kaviaar", "unexpected open item %q", str(it, "text"))
			}
		}
		check(t, open == 1, "open items = %d, want 1", open)

		prods := arr(t, "GET", "/v1/products?limit=200", nil, 200)
		var viaReceipt int
		for _, p := range prods {
			if str(p, "created_via") == "receipt" {
				viaReceipt++
			}
		}
		check(t, viaReceipt >= 1, "no products with created_via=receipt (%d products)", len(prods))
		note(t, "linked %d, created %d, review %d", num(ap, "linked"), num(ap, "created"), num(ap, "needs_review"))
	})

	step(t, "undo list checks of a receipt", func(t *testing.T) {
		needs(t, st.melkItem != 0, "confirm")
		res := obj(t, "POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", st.rid), nil, 200)
		check(t, len(list(res, "restored")) == 2, "restored %d items, want 2: %v", len(list(res, "restored")), res)
		for _, it := range arr(t, "GET", "/v1/list", nil, 200) {
			check(t, str(it, "checked_at") == "", "item %q still checked", str(it, "text"))
		}
		res = obj(t, "POST", fmt.Sprintf("/v1/receipts/%d/list-checks/undo", st.rid), nil, 200)
		check(t, len(list(res, "restored")) == 0, "second undo restored %d", len(list(res, "restored")))
	})

	var barcodes struct{ found, missing, down string }
	step(t, "barcode create (fake Open Food Facts on the host)", func(t *testing.T) {
		barcodes.found = ean13("871234500001")
		barcodes.missing = ean13("871234500002")
		barcodes.down = ean13("871234500003")
		fakeOFF.set(barcodes.found, "found")
		fakeOFF.set(barcodes.down, "down")

		c := obj(t, "POST", "/v1/barcodes/"+barcodes.found+"/product", nil, 201)
		check(t, str(c, "status") == "created" && c["created_now"] == true && str(c, "off_status") == "found", "found: %v", c)
		p, _ := c["product"].(M)
		check(t, strings.Contains(str(p, "name"), "Halfvolle melk") && str(p, "barcode") == barcodes.found, "product: %v", p)
		again := obj(t, "POST", "/v1/barcodes/"+barcodes.found+"/product", nil, 200)
		check(t, str(again, "status") == "known" && again["created_now"] == false, "repeat: %v", again)
		lk := obj(t, "GET", "/v1/barcodes/"+barcodes.found, nil, 200)
		check(t, lk != nil, "lookup")

		m := obj(t, "POST", "/v1/barcodes/"+barcodes.missing+"/product", nil, 201)
		mp, _ := m["product"].(M)
		check(t, str(m, "status") == "created_minimal" && str(m, "off_status") == "not_found" && mp["needs_review"] == true && str(mp, "review_reason") == "barcode_minimal", "missing: %v", m)
		d := obj(t, "POST", "/v1/barcodes/"+barcodes.down+"/product", nil, 201)
		dp, _ := d["product"].(M)
		check(t, str(d, "status") == "created_minimal" && str(d, "off_status") == "unavailable" && str(dp, "review_reason") == "barcode_off_unavailable", "down: %v", d)

		fakeOFF.set(barcodes.down, "found")
		ref := obj(t, "POST", fmt.Sprintf("/v1/products/%d/refresh", num(dp, "id")), nil, 200)
		check(t, str(ref, "off_status") == "found", "refresh after OFF is back: %v", ref)

		code, _, _, _ := raw("POST", "/v1/barcodes/123/product", nil, true)
		check(t, code == 400, "bad barcode = %d, want 400", code)
		fakeOFF.mu.Lock()
		hits := fakeOFF.hits[barcodes.found]
		fakeOFF.mu.Unlock()
		check(t, hits >= 1, "the API never asked the fake Open Food Facts server")
		note(t, "OFF = fake server on the host; real Open Food Facts is not contacted")
	})

	step(t, "review queue: counts, accept a card", func(t *testing.T) {
		needs(t, barcodes.missing != "", "barcode create")
		cat := obj(t, "POST", "/v1/categories", M{"name": "E2E Review Cat"}, 201)
		q := obj(t, "GET", "/v1/products/review-queue", nil, 200)
		total := num(q, "total")
		check(t, total >= 1, "queue empty after barcode_minimal products")
		counts, _ := q["counts"].(M)
		var sum int64
		for _, v := range counts {
			sum += int64(v.(float64))
		}
		check(t, sum == total, "counts sum %d != total %d: %v", sum, total, counts)
		check(t, num(counts, "barcode_minimal") >= 1, "counts: %v", counts)
		zero := obj(t, "GET", "/v1/products/review-queue?limit=0", nil, 200)
		check(t, num(zero, "total") == total && len(list(zero, "items")) == 0, "limit=0: %v", zero)

		var card M
		for _, it := range list(obj(t, "GET", "/v1/products/review-queue?reason=barcode_minimal", nil, 200), "items") {
			card, _ = it["product"].(M)
		}
		check(t, card != nil, "no barcode_minimal card")
		res := obj(t, "POST", fmt.Sprintf("/v1/products/%d/review", num(card, "id")), M{"action": "accept", "category_id": num(cat, "id"), "name": "E2E Plain Product"}, 200)
		p, _ := res["product"].(M)
		check(t, p["needs_review"] == false && str(p, "name") == "E2E Plain Product" && num(p, "category_id") == num(cat, "id"), "accepted: %v", p)
		q2 := obj(t, "GET", "/v1/products/review-queue", nil, 200)
		check(t, num(q2, "total") == total-1, "queue %d -> %d, want one less", total, num(q2, "total"))
	})

	step(t, "merge a product and undo it", func(t *testing.T) {
		needs(t, st.prodBanana != 0, "confirm")
		target := obj(t, "POST", "/v1/products", M{"name": "E2E Merge Target"}, 201)
		tid := num(target, "id")
		mr := obj(t, "POST", fmt.Sprintf("/v1/products/%d/merge", st.prodBanana), M{"into_product_id": tid}, 200)
		check(t, num(mr, "lines_moved") >= 1 && num(mr, "merge_id") > 0, "merge: %v", mr)
		check(t, lineProduct(t, st.lines[2]) == tid, "banana line product after merge = %d, want %d", lineProduct(t, st.lines[2]), tid)
		code, _ := status(t, "GET", fmt.Sprintf("/v1/products/%d", st.prodBanana), nil)
		check(t, code == 404, "merged-away product GET = %d, want 404", code)
		code, _ = status(t, "POST", fmt.Sprintf("/v1/products/%d/merge", tid), M{"into_product_id": tid})
		check(t, code == 422, "merge into itself = %d, want 422", code)

		un := obj(t, "POST", fmt.Sprintf("/v1/product-merges/%d/undo", num(mr, "merge_id")), nil, 200)
		check(t, str(un, "kind") == "merge" && num(un, "lines_restored") >= 1, "undo: %v", un)
		check(t, lineProduct(t, st.lines[2]) == st.prodBanana, "banana line product after undo = %d, want %d", lineProduct(t, st.lines[2]), st.prodBanana)
		obj(t, "GET", fmt.Sprintf("/v1/products/%d", st.prodBanana), nil, 200)
		code, _ = status(t, "POST", fmt.Sprintf("/v1/product-merges/%d/undo", num(mr, "merge_id")), nil)
		check(t, code == 409, "second undo = %d, want 409", code)
	})

	step(t, "groups and checklists: create, items, move, template, instantiate, delete + restore", func(t *testing.T) {
		g := obj(t, "POST", "/v1/list/groups", M{"client_id": "e2e-g1", "name": "E2E Weekend", "items": []M{{"text": "E2E tent"}, {"text": "E2E matches"}}}, 201)
		gid := num(g, "id")
		check(t, len(g["item_ids"].([]any)) == 2, "item_ids: %v", g["item_ids"])
		rep := obj(t, "POST", "/v1/list/groups", M{"client_id": "e2e-g1", "name": "E2E Weekend", "items": []M{{"text": "E2E tent"}, {"text": "E2E matches"}}}, 200)
		check(t, num(rep, "id") == gid, "repeat made a second group")
		got := obj(t, "GET", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
		check(t, len(list(got, "items")) == 2 && str(got, "kind") == "checklist", "group: %v", got)

		rope := obj(t, "POST", "/v1/list", M{"text": "E2E rope", "group_id": gid}, 201)
		check(t, num(rope, "group_id") == gid, "rope: %v", rope)
		ck := obj(t, "POST", fmt.Sprintf("/v1/list/%d/check", num(rope, "id")), nil, 200)
		check(t, str(ck, "checked_at") != "", "check: %v", ck)
		got = obj(t, "GET", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
		check(t, num(got, "item_count") == 3 && num(got, "checked_count") == 1, "counts: %v", got)
		mv := obj(t, "POST", fmt.Sprintf("/v1/list/%d/move", num(rope, "id")), M{"group_id": nil}, 200)
		check(t, mv["group_id"] == nil, "moved to main list: %v", mv)

		tpl := obj(t, "POST", fmt.Sprintf("/v1/list/groups/%d/save-as-template", gid), M{"name": "E2E Weekend template"}, 201)
		check(t, str(tpl, "kind") == "template", "template: %v", tpl)
		tpls := arr(t, "GET", "/v1/list/groups?kind=template", nil, 200)
		check(t, len(tpls) == 1, "templates: %d", len(tpls))
		inst := obj(t, "POST", fmt.Sprintf("/v1/list/groups/%d/instantiate", num(tpl, "id")), M{"name": "E2E Weekend again"}, 201)
		check(t, str(inst, "kind") == "checklist" && len(inst["item_ids"].([]any)) == 2, "instantiate: %v", inst)

		ch := obj(t, "GET", "/v1/list/changes?since_rev=0", nil, 200)
		check(t, ch["full_resync"] == true && len(list(ch, "groups")) >= 3 && num(ch, "rev") > 0, "changes: full_resync=%v groups=%d", ch["full_resync"], len(list(ch, "groups")))

		del := obj(t, "DELETE", fmt.Sprintf("/v1/list/groups/%d", gid), nil, 200)
		check(t, del != nil, "delete group")
		code, _ := status(t, "GET", fmt.Sprintf("/v1/list/groups/%d", gid), nil)
		check(t, code == 404, "deleted group GET = %d, want 404", code)
		back := obj(t, "POST", fmt.Sprintf("/v1/list/groups/%d/restore", gid), nil, 200)
		check(t, num(back, "id") == gid, "restore: %v", back)
		obj(t, "DELETE", fmt.Sprintf("/v1/list/groups/%d?items=delete", num(inst, "id")), nil, 200)
		obj(t, "DELETE", fmt.Sprintf("/v1/list/groups/%d", num(tpl, "id")), nil, 200)
	})

	step(t, "list/plan: not_enough_data on thin data, settings", func(t *testing.T) {
		p := obj(t, "GET", "/v1/list/plan", nil, 200)
		check(t, str(p, "status") == "not_enough_data", "plan status %q, want not_enough_data: %v", str(p, "status"), p)
		s := obj(t, "GET", "/v1/list/plan/settings", nil, 200)
		check(t, num(s, "extra_stop_cents") == 250 && num(s, "max_stops") == 2, "default settings: %v", s)
		s = obj(t, "PATCH", "/v1/list/plan/settings", M{"extra_stop_cents": 300}, 200)
		check(t, num(s, "extra_stop_cents") == 300, "patched: %v", s)
		obj(t, "GET", "/v1/list/plan?extra_stop_cents=10", nil, 200) // what-if saves nothing
		s = obj(t, "GET", "/v1/list/plan/settings", nil, 200)
		check(t, num(s, "extra_stop_cents") == 300, "what-if changed settings: %v", s)
		code, _ := status(t, "PATCH", "/v1/list/plan/settings", M{"extra_stop_cents": 5000})
		check(t, code == 400, "out of range = %d, want 400", code)
		obj(t, "PATCH", "/v1/list/plan/settings", M{"extra_stop_cents": 250}, 200)
	})

	step(t, "categories: CRUD, delete-with-move", func(t *testing.T) {
		a := obj(t, "POST", "/v1/categories", M{"name": "E2E Fruit"}, 201)
		code, _ := status(t, "POST", "/v1/categories", M{"name": "e2e fruit"})
		check(t, code == 409, "duplicate name (case-insensitive) = %d, want 409", code)
		b := obj(t, "POST", "/v1/categories", M{"name": "E2E Dairy"}, 201)
		aid, bid := num(a, "id"), num(b, "id")
		p := obj(t, "POST", "/v1/products", M{"name": "E2E Pear", "category_id": aid}, 201)
		pid := num(p, "id")
		cats := arr(t, "GET", "/v1/categories", nil, 200)
		check(t, catCount(cats, aid) == 1, "product_count(A) = %d, want 1", catCount(cats, aid))

		code, prob := status(t, "DELETE", fmt.Sprintf("/v1/categories/%d", aid), nil)
		check(t, code == 409 && str(prob, "code") == "category_has_products", "delete without move_to = %d %v", code, prob)
		code, _ = status(t, "DELETE", fmt.Sprintf("/v1/categories/%d?move_to=%d", aid, bid), nil)
		check(t, code == 204, "delete with move_to = %d, want 204", code)
		pp := obj(t, "GET", fmt.Sprintf("/v1/products/%d", pid), nil, 200)
		check(t, num(pp, "category_id") == bid, "product moved to %d, want %d", num(pp, "category_id"), bid)
		cats = arr(t, "GET", "/v1/categories", nil, 200)
		check(t, catCount(cats, aid) < 0 && catCount(cats, bid) == 1, "after move: A gone=%v, B count=%d", catCount(cats, aid) < 0, catCount(cats, bid))

		r := obj(t, "PATCH", fmt.Sprintf("/v1/categories/%d", bid), M{"name": "E2E Dairy & eggs"}, 200)
		check(t, str(r, "name") == "E2E Dairy & eggs", "rename: %v", r)
		code, _ = status(t, "DELETE", fmt.Sprintf("/v1/categories/%d?move_to=0", bid), nil)
		check(t, code == 204, "delete with move_to=0 = %d", code)
		pp = obj(t, "GET", fmt.Sprintf("/v1/products/%d", pid), nil, 200)
		check(t, pp["category_id"] == nil, "product category after move_to=0: %v", pp["category_id"])
		c := obj(t, "POST", "/v1/categories", M{"name": "E2E Empty"}, 201)
		code, _ = status(t, "DELETE", fmt.Sprintf("/v1/categories/%d", num(c, "id")), nil)
		check(t, code == 204, "delete empty category = %d, want 204", code)
	})

	step(t, "backup run + health status (no export endpoint exists)", func(t *testing.T) {
		out, err := compose(ctxBG(), "", "run", "--rm", "--no-deps", "backup", "backup")
		check(t, err == nil, "backup run: %v: %s", err, trunc([]byte(out)))
		code, h := status(t, "GET", "/healthz", nil)
		checks, _ := h["checks"].(M)
		bk, _ := checks["backup"].(M)
		check(t, code == 200 && bk != nil && bk["ok"] == true, "healthz after backup: %d %v", code, h)
		note(t, "the API has no export route; backup status is checked via /healthz")
	})

	step(t, "private photos IMG_1463..1475 (spike/data, git-ignored)", func(t *testing.T) {
		photos := findPhotos()
		if len(photos) == 0 {
			skip(t, "no JPEGs in spike/data or spike/receipts (private, git-ignored); set E2E_PHOTOS=<dir> to use another folder")
		}
		type up struct {
			name string
			id   int64
		}
		var ups []up
		for _, p := range photos {
			data, err := os.ReadFile(p)
			check(t, err == nil, "read %s: %v", p, err)
			code, r := upload(t, filepath.Base(p), data, "image/jpeg", nil)
			check(t, code == 202, "upload %s = %d %v", p, code, r)
			ups = append(ups, up{filepath.Base(p), num(r, "id")})
		}
		var bad []string
		knownCount := 0
		known := regexp.MustCompile(`(?i)IMG_14(6[3-9]|7[0-4])`)
		for _, u := range ups {
			r := waitReceipt(t, u.id, 12*time.Minute)
			okTotals := num(r, "parsed_sum_cents") == num(r, "total_cents") && num(r, "total_cents") > 0
			if known.MatchString(u.name) {
				knownCount++
			}
			if known.MatchString(u.name) && !(str(r, "status") == "ok" && okTotals) {
				bad = append(bad, fmt.Sprintf("%s: status %s total %d parsed %d %v", u.name, str(r, "status"), num(r, "total_cents"), num(r, "parsed_sum_cents"), r["review_reasons"]))
			}
			if strings.Contains(strings.ToUpper(u.name), "IMG_1475") {
				// documented in docs/BACKEND-STATUS.md: curled photo, printed 45,04 vs parsed 43,57
				note(t, "IMG_1475 (known curled photo): status %s total %d parsed %d", str(r, "status"), num(r, "total_cents"), num(r, "parsed_sum_cents"))
			}
			code, _ := status(t, "GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", u.id), nil)
			check(t, code == 200, "shop-suggestion for %s = %d", u.name, code)
		}
		sort.Strings(bad)
		check(t, len(bad) == 0, "known-good receipts that do not reconcile: %s", strings.Join(bad, "; "))
		note(t, "%d photos uploaded, %d of them IMG_1463..1474 and all reconcile (printed total = parsed sum)", len(ups), knownCount)
	})
}

// ---- small helpers ---------------------------------------------------------------

func amsterdam() *time.Location {
	l, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		return time.UTC
	}
	return l
}

func dayOf(ts string) string {
	tm, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return tm.In(amsterdam()).Format("2006-01-02")
}

func lineTexts(r M) []string {
	var out []string
	for _, l := range list(r, "lines") {
		out = append(out, fmt.Sprintf("%s|%s|%d", str(l, "kind"), str(l, "raw_text"), num(l, "line_total_cents")))
	}
	return out
}

func lineProduct(t *testing.T, line M) int64 {
	t.Helper()
	r := obj(t, "GET", fmt.Sprintf("/v1/receipts/%d", st.rid), nil, 200)
	for _, l := range list(r, "lines") {
		if num(l, "id") == num(line, "id") {
			return num(l, "product_id")
		}
	}
	check(t, false, "line %d vanished", num(line, "id"))
	return 0
}

func catCount(cats []M, id int64) int64 {
	for _, c := range cats {
		if num(c, "id") == id {
			return num(c, "product_count")
		}
	}
	return -1
}

func findPhotos() []string {
	dirs := []string{filepath.Join(repoRoot, "spike/data"), filepath.Join(repoRoot, "spike/receipts")}
	if d := os.Getenv("E2E_PHOTOS"); d != "" {
		dirs = []string{d}
	}
	var out []string
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			n := strings.ToLower(e.Name())
			if !e.IsDir() && (strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")) {
				out = append(out, filepath.Join(d, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}
