package autoprod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func dump(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT * FROM products ORDER BY id`,
		`SELECT id, product_id FROM receipt_lines ORDER BY id`,
		`SELECT * FROM product_aliases ORDER BY id`,
		`SELECT * FROM nutrition ORDER BY product_id`,
	} {
		rows, err := conn.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for _, v := range vals {
				fmt.Fprintf(&b, "%v|", v)
			}
			b.WriteString("\n")
		}
		rows.Close()
	}
	return b.String()
}

func idByName(t *testing.T, conn *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(`SELECT id FROM products WHERE name = ? AND deleted_at IS NULL`, name).Scan(&id); err != nil {
		t.Fatalf("product %q: %v", name, err)
	}
	return id
}

func TestMergeUndoIsExact(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	processAll(t, s, loadReceipts(t, conn), "receipt")

	arla, bio := idByName(t, conn, "Arla Melk"), idByName(t, conn, "Arla Bio Halfv. Melk")
	// give the survivor and the loser something to copy so the "empty fields" path is exercised
	if _, err := conn.Exec(`UPDATE products SET brand='Arla', net_quantity=1000, net_unit='ml', off_code='x1' WHERE id=?`, arla); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO nutrition (product_id, per, energy_kcal, source) VALUES (?, '100ml', 46, 'manual')`, arla); err != nil {
		t.Fatal(err)
	}
	before := dump(t, conn)

	res, err := s.Merge(ctx, arla, bio)
	if err != nil {
		t.Fatal(err)
	}
	if res.Product.ID != bio || res.MergedAway.ID != arla || res.LinesMoved == 0 {
		t.Fatalf("merge result = %+v", res)
	}
	if res.Product.Brand.String != "Arla" || res.Product.NetQuantity.Float64 != 1000 {
		t.Errorf("empty fields were not copied to the survivor: %+v", res.Product)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, arla); n != 0 {
		t.Errorf("%d lines still on the merged-away product", n)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE id = ? AND deleted_at IS NOT NULL AND merged_into_id = ?`, arla, bio); n != 1 {
		t.Error("merged-away product is not soft-deleted with merged_into_id")
	}

	u, err := s.Undo(ctx, res.MergeID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Kind != "merge" || u.Product.ID != arla || u.Into.ID != bio || u.LinesRestored != res.LinesMoved || u.LinesSkipped != 0 {
		t.Fatalf("undo = %+v", u)
	}
	if after := dump(t, conn); after != before {
		t.Errorf("state after merge+undo differs from before:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if _, err := s.Undo(ctx, res.MergeID); !errors.Is(err, ErrConflict) {
		t.Errorf("second undo err = %v; want conflict", err)
	}
}

func TestUndoSkipsRelinkedLinesAndExpires(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	processAll(t, s, loadReceipts(t, conn), "receipt")
	arla, bio := idByName(t, conn, "Arla Melk"), idByName(t, conn, "Arla Bio Halfv. Melk")
	res, err := s.Merge(ctx, arla, bio)
	if err != nil {
		t.Fatal(err)
	}
	// the user relinks one moved line to a third product after the merge
	var lineID int64
	_ = conn.QueryRow(`SELECT json_extract(line_ids, '$[0]') FROM product_merges WHERE id = ?`, res.MergeID).Scan(&lineID)
	other := idByName(t, conn, "Fanta")
	if _, err := conn.Exec(`UPDATE receipt_lines SET product_id = ? WHERE id = ?`, other, lineID); err != nil {
		t.Fatal(err)
	}
	u, err := s.Undo(ctx, res.MergeID)
	if err != nil {
		t.Fatal(err)
	}
	if u.LinesSkipped != 1 {
		t.Errorf("lines_skipped = %d; want 1", u.LinesSkipped)
	}

	res2, err := s.Merge(ctx, arla, bio)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if _, err := s.Undo(ctx, res2.MergeID); !errors.Is(err, ErrGone) {
		t.Errorf("undo after 31 days err = %v; want gone", err)
	}
}

func TestMergeRefusesDifferentBarcodesAndSelf(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	processAll(t, s, loadReceipts(t, conn), "receipt")
	a, b := idByName(t, conn, "Arla Melk"), idByName(t, conn, "Fanta")
	_, _ = conn.Exec(`UPDATE products SET barcode='8711111111111' WHERE id=?`, a)
	_, _ = conn.Exec(`UPDATE products SET barcode='8722222222222' WHERE id=?`, b)
	if _, err := s.Merge(ctx, a, b); !errors.Is(err, ErrDifferentBarcodes) {
		t.Errorf("err = %v; want different barcodes", err)
	}
	if _, err := s.Merge(ctx, a, a); !errors.Is(err, ErrInvalid) {
		t.Errorf("self merge err = %v; want invalid", err)
	}
}

func TestDiscardAndUndo(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	processAll(t, s, loadReceipts(t, conn), "receipt")
	p := idByName(t, conn, "Fanta")
	before := dump(t, conn)
	res, err := s.Discard(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "discard" || res.LinesMoved == 0 {
		t.Fatalf("discard = %+v", res)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM product_aliases WHERE product_id = ?`, p); n != 0 {
		t.Error("aliases of a discarded product must go")
	}
	u, err := s.Undo(ctx, res.MergeID)
	if err != nil || u.Kind != "discard" || u.Product.ID != p {
		t.Fatalf("undo discard = %+v, %v", u, err)
	}
	if after := dump(t, conn); after != before {
		t.Errorf("discard+undo changed the data:\n%s\n---\n%s", before, after)
	}
	// manual products cannot be discarded
	_, _ = conn.Exec(`UPDATE products SET created_via='manual' WHERE id=?`, p)
	if _, err := s.Discard(ctx, p); !errors.Is(err, ErrInvalid) {
		t.Errorf("discard manual err = %v; want invalid", err)
	}
}

func TestSplitAndUndo(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	processAll(t, s, loadReceipts(t, conn), "receipt")
	p := idByName(t, conn, "Coca-Cola")
	var lineID int64
	if err := conn.QueryRow(`SELECT id FROM receipt_lines WHERE product_id = ? AND kind='item' LIMIT 1`, p).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	linesBefore := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, p)
	res, err := s.Split(ctx, lineID, "Coca-Cola Zero")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "split" || res.Product.Name != "Coca-Cola Zero" || res.Product.CreatedVia != "manual" || res.Product.NeedsReview != 0 || res.LinesMoved < 1 {
		t.Fatalf("split = %+v", res)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, res.Product.ID); n != res.LinesMoved {
		t.Errorf("%d lines on the new product, moved %d", n, res.LinesMoved)
	}
	if _, err := s.Undo(ctx, res.MergeID); err != nil {
		t.Fatal(err)
	}
	// the new product stays as a soft-deleted row; the lines are back where they were
	if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, p); n != linesBefore {
		t.Errorf("%d lines on the original product after undo, had %d", n, linesBefore)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE product_id = ?`, res.Product.ID); n != 0 {
		t.Errorf("%d lines left on the undone split product", n)
	}
	if _, err := s.Split(ctx, 999999, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("split missing line err = %v", err)
	}
}
