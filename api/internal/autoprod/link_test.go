package autoprod

import (
	"context"
	"testing"
)

func processAll(t *testing.T, s *Service, ids []int64, via string) Stats {
	t.Helper()
	var total Stats
	for _, id := range ids {
		st, err := s.ProcessReceipt(context.Background(), id, via)
		if err != nil {
			t.Fatal(err)
		}
		total.add(st)
	}
	return total
}

func productOfLine(t *testing.T, s *Service, rawLike string) int64 {
	t.Helper()
	var id int64
	if err := s.conn.QueryRow(`SELECT product_id FROM receipt_lines WHERE raw_text LIKE ? AND product_id IS NOT NULL LIMIT 1`, rawLike).Scan(&id); err != nil {
		t.Fatalf("no linked line like %q: %v", rawLike, err)
	}
	return id
}

func TestCreateOrLinkOnTheThirteenRealReceipts(t *testing.T) {
	conn := newDB(t)
	s := newService(t, conn, true)
	ids := loadReceipts(t, conn)
	st := processAll(t, s, ids, "receipt")
	products := count(t, conn, `SELECT COUNT(*) FROM products`)
	t.Logf("linked %d created %d to-review %d skipped %d; %d products", st.Linked, st.Created, st.NeedsReview, st.Skipped, products)

	if products != st.Created {
		t.Errorf("%d products but %d created", products, st.Created)
	}
	if products < 55 || products > 80 {
		t.Errorf("%d products; expect roughly 60-75 from 99 item lines", products)
	}
	// nothing is left unlinked except skipped junk
	if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE kind='item' AND product_id IS NULL`); n != st.Skipped {
		t.Errorf("%d unlinked item lines, %d skipped", n, st.Skipped)
	}
	// weight rows are never products
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE name LIKE '%kg%'`); n != 0 {
		t.Errorf("%d products made from weight rows", n)
	}

	// OCR junk in front of the name: the twins link to the clean product
	same := func(a, b string) {
		t.Helper()
		if pa, pb := productOfLine(t, s, a), productOfLine(t, s, b); pa != pb {
			t.Errorf("%q (product %d) and %q (product %d) should be the same product", a, pa, b, pb)
		}
	}
	same("HHTHN AH ZALMFILET%", "AH ZALMFILET")
	same("TTNN FIT PIGGY%", "FIT PIGGY")
	same("%COCA-COLA%", "%COCA-COLA%")

	// the six wrong pairs from the spec must not be auto-linked
	for _, pair := range [][2]string{
		{"AH BIO MELK%", "AH BIO SAP%"}, {"ARLA BIO HALFV. MELK%", "ARLA MELK%"}, {"AH BROCCOLI%", "AH BIO SAP%"},
		{"AH PEPERMIX%", "AH HAVERMOUT%"}, {"AH PLUOT%", "%AH SPRUITJES%"}, {"KIP LOPPOOT%", "KIP POTEN%"},
	} {
		var a, b int64
		if conn.QueryRow(`SELECT product_id FROM receipt_lines WHERE raw_text LIKE ? AND product_id IS NOT NULL LIMIT 1`, pair[0]).Scan(&a) != nil ||
			conn.QueryRow(`SELECT product_id FROM receipt_lines WHERE raw_text LIKE ? AND product_id IS NOT NULL LIMIT 1`, pair[1]).Scan(&b) != nil {
			continue // that spelling is not in this parse of the receipts
		}
		if a == b {
			t.Errorf("wrongly linked %q with %q", pair[0], pair[1])
		}
	}

	// odd name flagged
	var reason string
	if err := conn.QueryRow(`SELECT review_reason FROM products WHERE name LIKE 'Nunnunonntt%'`).Scan(&reason); err != nil || reason != ReasonOddName {
		t.Errorf("odd product reason = %q, %v; want %s", reason, err, ReasonOddName)
	}

	// idempotent: a second pass over the same receipts and a backfill create nothing
	again := processAll(t, s, ids, "backfill")
	if again.Created != 0 || again.Linked != 0 {
		t.Errorf("second pass: %+v; want nothing new", again)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != products {
		t.Errorf("products %d -> %d on the second pass", products, n)
	}
}
