package shopsuggest

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"shop/internal/ocr"
	"shop/internal/store"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := store.Migrate(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func addShop(t *testing.T, conn *sql.DB, name, chain, branch, address string) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO shops (name, chain, branch, address, created_at, updated_at) VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), 't', 't')`, name, chain, branch, address)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// addReceipt stores an unassigned, unconfirmed receipt whose OCR output has these rows.
func addReceipt(t *testing.T, conn *sql.DB, rows ...string) int64 {
	t.Helper()
	var lines []ocr.Line
	for i, r := range rows {
		y := float64(40 + i*46)
		lines = append(lines, ocr.Line{Text: r, Conf: 0.98, Box: [4]float64{30, y, 300, y + 30}})
	}
	raw, _ := json.Marshal(ocr.Result{Engine: "test", Lines: lines})
	res, err := conn.Exec(`INSERT INTO receipts (shop_id, image_path, image_sha256, status, raw_ocr_json, created_at, updated_at)
		VALUES (NULL, ?, ?, 'ok', ?, 't', 't')`, string(raw[:20])+rows[0], string(raw), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func suggest(t *testing.T, s *Service, id int64) *Suggestion {
	t.Helper()
	sg, err := s.Suggest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if sg == nil {
		t.Fatalf("receipt %d: no suggestion", id)
	}
	return sg
}

func sameCard(t *testing.T, a, b *Suggestion) {
	t.Helper()
	if a.Key != b.Key || a.Kind != b.Kind || a.Name != b.Name || a.Branch != b.Branch || a.Domain != b.Domain ||
		(a.ShopID == nil) != (b.ShopID == nil) || (a.ShopID != nil && *a.ShopID != *b.ShopID) {
		t.Errorf("two cards for one shop:\n  %+v\n  %+v", a, b)
	}
}

func TestExistingShopSuggestionIsIdenticalForEveryReceipt(t *testing.T) {
	conn := testDB(t)
	s := NewService(conn)
	addShop(t, conn, "Dirk", "Dirk", "Marktweg 68-70, Teststad", "Marktweg 68-70")
	// the same Dirk, read with different OCR spellings of the street
	a := addReceipt(t, conn, "Dirk", "Dirk van den Broek Marktweg 68-70", "1234 AB Teststad", "Kassabon")
	b := addReceipt(t, conn, "Dirk", "Dirk van den Broek Marktwag 68-70", "1234 AB Teststad", "Kassabon")
	c := addReceipt(t, conn, "Dirk", "Dirk van Don Broek Marktweg 68-70", "1234 AB Teststad", "Kassabon")
	sa, sb, sc := suggest(t, s, a), suggest(t, s, b), suggest(t, s, c)
	if sa.Kind != "existing" || sa.Name != "Dirk" || sa.Branch != "Marktweg 68-70, Teststad" || sa.Key == "" {
		t.Fatalf("suggestion = %+v; want the shop's own name and branch", sa)
	}
	sameCard(t, sa, sb)
	sameCard(t, sa, sc)
	if len(sa.Waiting) != 2 {
		t.Errorf("waiting = %v; want the two other receipts", sa.Waiting)
	}
}

func TestNewShopIsNeverOfferedWhenAnExistingShopMatches(t *testing.T) {
	for _, c := range []struct{ shopName, chain, header string }{
		{"Dirk van den Broek", "", "Dirk"}, // longer name
		{"dirk", "", "Dirk"},               // case
		{"AH", "", "Albert Heijn"},         // abbreviation of a built-in chain
		{"Albert Heijn Zuid", "Albert Heijn", "Albert Heijn"},
		{"Supermarkt X", "Dirk", "Dirk"}, // chain column
		{"Jumbo Rijnmond", "", "Jumbo"},  // chain with a suffix
	} {
		conn := testDB(t)
		s := NewService(conn)
		addShop(t, conn, c.shopName, c.chain, "", "")
		id := addReceipt(t, conn, c.header, "Hoofdstraat 12", "1234 AB Utrecht", "MELK 1,29")
		sg := suggest(t, s, id)
		if sg.Kind != "existing" {
			t.Errorf("shop %q / header %q: got a %s suggestion %q; want the existing shop", c.shopName, c.header, sg.Kind, sg.Name)
		}
	}
	// a genuinely different shop is still offered as new
	conn := testDB(t)
	s := NewService(conn)
	addShop(t, conn, "Dirk", "Dirk", "", "")
	id := addReceipt(t, conn, "Jumbo", "Hoofdstraat 12", "1234 AB Utrecht", "MELK 1,29")
	if sg := suggest(t, s, id); sg.Kind != "new" || sg.Name != "Jumbo" {
		t.Errorf("different chain: %+v", sg)
	}
}

func TestNewShopSuggestionIsOneCardForTheBranch(t *testing.T) {
	conn := testDB(t)
	s := NewService(conn)
	// the same unknown shop; OCR spells the street differently and the order of upload is arbitrary
	first := addReceipt(t, conn, "Istanbul", "SUPERMARKT", "Marktweg 37", "1234 Teststad", "MELK 1,29")
	second := addReceipt(t, conn, "Istanbul", "SUPERMARKT", "Marktwag 37", "1234 Teststad", "KAAS 2,29")
	third := addReceipt(t, conn, "istanbul", "SUPERMARKT", "Marktweg 37", "1234 Teststad", "BROOD 1,99")
	sf, ss, st := suggest(t, s, first), suggest(t, s, second), suggest(t, s, third)
	if sf.Kind != "new" || sf.Name != "Istanbul Supermarkt" {
		t.Fatalf("suggestion = %+v", sf)
	}
	sameCard(t, sf, ss)
	sameCard(t, sf, st)
	if sf.Branch != "Marktweg 37, Teststad" {
		t.Errorf("the canonical spelling is the oldest receipt's, got %q", sf.Branch)
	}
	// accepting from the LAST receipt creates the shop with that same spelling and moves all three
	shop, moved, err := s.Accept(context.Background(), third, nil, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 3 || shop.Branch.String != "Marktweg 37, Teststad" || shop.Name != "Istanbul Supermarkt" {
		t.Errorf("accept: shop %q branch %q moved %v", shop.Name, shop.Branch.String, moved)
	}
	// now they are all on a shop: no suggestion left
	if sg, _ := s.Suggest(context.Background(), first); sg != nil {
		t.Errorf("suggestion after accepting: %+v", sg)
	}
}

func TestTwoBranchesOfOneChainPickTheRightShop(t *testing.T) {
	conn := testDB(t)
	s := NewService(conn)
	n := addShop(t, conn, "Dirk Noord", "Dirk", "Marktweg 68-70, Teststad", "Marktweg 68-70")
	z := addShop(t, conn, "Dirk Zuid", "Dirk", "Hoofdlaan 5, Vlaardingen", "Hoofdlaan 5")
	a := addReceipt(t, conn, "Dirk", "Marktwag 68-70", "1234 AB Teststad", "MELK 1,29")
	b := addReceipt(t, conn, "Dirk", "Hoofdlaan 5", "3131 AB Vlaardingen", "MELK 1,29")
	if sg := suggest(t, s, a); sg.Kind != "existing" || *sg.ShopID != n {
		t.Errorf("Teststad receipt: %+v", sg)
	}
	if sg := suggest(t, s, b); sg.Kind != "existing" || *sg.ShopID != z {
		t.Errorf("Vlaardingen receipt: %+v", sg)
	}
}
